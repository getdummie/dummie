package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

// The browser terminal. The page itself is served by this server -- it is the
// origin the user is already signed in to, and the one place the terminal's
// javascript can come from -- while the shell it talks to lives behind the
// host's proxy at "<vm>.shell.<domain>".
//
// That split is why this endpoint exists. The websocket the page opens is
// cross-origin, and a cross-site handshake carries no cookie, so the proxy's
// session cookie cannot be the credential there the way it is for a VM's own
// hostname. Instead this hands the page a token scoped to the console hostname,
// signed with the key every host already holds, and the page presents it on the
// websocket url.
//
// It is the same key, the same payload shape and the same ownership check as
// GET /login; only the audience differs, and that difference is what stops a
// token that opens a shell being spent as an ordinary web session and vice
// versa (see the proxy's Verify/VerifyConsole).

// proxyConsoleAudPrefix marks a token as one that may open a shell. It must
// match consoleAudPrefix in the proxy's authenticator: the two sides never
// exchange it, they only both have to spell it the same way.
const proxyConsoleAudPrefix = "console:"

// proxyConsoleLabel lives in proxy_config.go, next to the generated console
// block that teaches the proxy the same rule.

// consoleHost is where a VM's terminal answers: its name, the console label and
// the domain of the host it runs on. "" when that host has no domain, which is
// the same condition that leaves vmURL empty -- without one there is no name to
// route on and nothing to sign a token for.
func (h *UserHandler) consoleHost(ctx context.Context, v db.Vm) string {
	tld := h.vmDomainTLD(ctx, v)
	if tld == "" {
		return ""
	}
	return fmt.Sprintf("%s.%s.%s", v.Name, proxyConsoleLabel, tld)
}

// consoleURL is the websocket endpoint for a VM's terminal.
func (h *UserHandler) consoleURL(ctx context.Context, v db.Vm) string {
	host := h.consoleHost(ctx, v)
	if host == "" {
		return ""
	}
	// ws on a dev control plane for the same reason vmURL is http there: guests
	// are served over plaintext, and wss to a host that does not terminate TLS
	// fails in the browser with nothing useful to show the user.
	scheme := "ws"
	if h.prod {
		scheme = "wss"
	}
	return fmt.Sprintf("%s://%s/", scheme, host)
}

// consoleTokenDTO is what the terminal page needs to open its socket.
type consoleTokenDTO struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	// ExpiresIn lets the page mint a fresh token before reconnecting rather than
	// discovering the old one died in the middle of a handshake.
	ExpiresIn int `json:"expires_in"`
}

// ConsoleToken mints a token for one VM's terminal.
//
// POST rather than GET: it is a credential, and a GET would put it in browser
// history, in referrers and in any log that records request lines.
//
// @Summary     Mint a console token
// @Description Returns the websocket url for this VM's terminal and a short-lived token that opens it. POST rather than GET because it is a credential, and a GET would put it in browser history, in referrers and in any log that records request lines. The url in vmDTO.console_url is not itself a credential -- it needs one of these.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     200 {object} consoleTokenDTO
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError "the host this vm runs on has no domain, so it has no console"
// @Failure     503 {object} apiError "the console is not configured on this installation"
// @Router      /vms/{id}/console-token [post]
func (h *UserHandler) ConsoleToken(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	if h.proxy.secret == "" {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "the console is not configured on this installation")
	}
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
	}

	ctx := c.Request().Context()
	// Scoped to the caller by the query itself, so this cannot mint a token for
	// someone else's guest. Someone else's VM and a VM that does not exist are the
	// same answer here, as they are everywhere else on these routes.
	v, err := h.q.GetVMForOwner(ctx, db.GetVMForOwnerParams{ID: pgID, CreatedBy: owner})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "no such vm")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read vm")
	}

	host := h.consoleHost(ctx, v)
	if host == "" {
		return echo.NewHTTPError(http.StatusConflict, "the host this vm runs on has no domain, so it has no console")
	}

	u, err := h.q.GetUserByID(ctx, owner)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the caller")
	}

	// The subject is who the host will log as having opened the shell, and the
	// audience is the one hostname this token works on.
	token, err := mintProxyToken(h.proxy.secret, u.Email, proxyConsoleAudPrefix+host, time.Now())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not mint token")
	}
	return c.JSON(http.StatusOK, consoleTokenDTO{
		URL:       h.consoleURL(ctx, v),
		Token:     token,
		ExpiresIn: int(proxyTokenTTL.Seconds()),
	})
}
