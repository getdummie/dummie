package main

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

// A framed guest, for the workspace view.
//
// The problem is the same one the console has, in a different shape. A VM answers
// on its own domain, deliberately not this one -- guests run code their owner
// does not necessarily trust, and a separate origin is what stops that code
// reading this origin's storage, its dom or its session. But a frame on another
// site sends no cookie, so the proxy's session for the guest is not there, and
// neither is this server's session for the /login redirect that would mint one:
// the frame would be bounced to a sign-in it cannot complete.
//
// So the session is established before the frame is pointed anywhere. This mints
// the same token /login hands out -- same key, same audience, same ownership
// check -- and puts it on the proxy's callback url. The frame's first request
// redeems it for a session cookie, and every request after that carries that
// cookie, which is what makes the guest's own assets and fetches work rather than
// only the first document.
//
// In production the cookie the proxy sets is SameSite=None; Secure; Partitioned
// (see the generated auth block), so it is sent from the frame and is keyed to
// the page doing the framing. In dev, where guests are plaintext and no browser
// will keep such a cookie, it stays Lax -- which works as long as this server is
// reached under the same domain the guests are on.

// proxyAuthCallbackPath mirrors CallbackPath in the proxy. Like the console
// audience prefix it is never exchanged between the two sides: both just have to
// spell it the same way.
const proxyAuthCallbackPath = "/__auth/callback"

// webSessionDTO is where the workspace view points a frame.
type webSessionDTO struct {
	URL string `json:"url"`
	// ExpiresIn is the token's life, not the session's. The page mints a fresh one
	// per load rather than reusing this, so it only matters as the window in which
	// a load has to happen.
	ExpiresIn int `json:"expires_in"`
}

// WebSession mints a token for one VM's site and returns the url that redeems it.
//
// POST rather than GET for the same reason as the console token: it is a
// credential, and a GET would put it in history, in referrers and in any log that
// records request lines.
//
// @Summary     Open a session on a VM's site
// @Description Returns a url that redeems a short-lived token for a session on whatever this VM publishes over http. POST for the same reason as the console token: it is a credential. expires_in is the token's life, not the session's -- it is the window in which the url has to be loaded.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     200 {object} webSessionDTO
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError "the host this vm runs on has no domain, so it publishes nothing"
// @Failure     503 {object} apiError "guest sessions are not configured on this installation"
// @Router      /vms/{id}/web-session [post]
func (h *UserHandler) WebSession(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	if h.proxy.secret == "" {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "guest sessions are not configured on this installation")
	}
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
	}

	ctx := c.Request().Context()
	// Scoped to the caller by the query, so this cannot mint a session for someone
	// else's guest. Someone else's VM and a VM that does not exist are the same
	// answer here, as they are everywhere else on these routes.
	v, err := h.q.GetVMForOwner(ctx, db.GetVMForOwnerParams{ID: pgID, CreatedBy: owner})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "no such vm")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read vm")
	}

	base := h.vmURL(ctx, v)
	if base == "" {
		return echo.NewHTTPError(http.StatusConflict, "the host this vm runs on has no domain, so it publishes nothing")
	}
	target, err := url.Parse(base)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not build the guest url")
	}

	u, err := h.q.GetUserByID(ctx, owner)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the caller")
	}

	// The audience is the bare hostname, which is what makes this a session token
	// rather than a console one: the proxy verifies the two with the same key and
	// tells them apart by exactly this (see its Verify/VerifyConsole).
	token, err := mintProxyToken(h.proxy.secret, u.Email, target.Hostname(), time.Now())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not mint token")
	}

	target.Path = proxyAuthCallbackPath
	target.RawQuery = url.Values{"token": {token}, "next": {"/"}}.Encode()
	return c.JSON(http.StatusOK, webSessionDTO{
		URL:       target.String(),
		ExpiresIn: int(proxyTokenTTL.Seconds()),
	})
}
