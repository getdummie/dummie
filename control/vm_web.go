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

const proxyAuthCallbackPath = "/__auth/callback"

type webSessionDTO struct {
	URL string `json:"url"`
	ExpiresIn int `json:"expires_in"`
}

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
