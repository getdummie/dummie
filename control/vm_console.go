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

const proxyConsoleAudPrefix = "console:"

func (h *UserHandler) consoleHost(ctx context.Context, v db.Vm) string {
	tld := h.vmDomainTLD(ctx, v)
	if tld == "" {
		return ""
	}
	return fmt.Sprintf("%s.%s.%s", v.Name, proxyConsoleLabel, tld)
}

func (h *UserHandler) consoleURL(ctx context.Context, v db.Vm) string {
	host := h.consoleHost(ctx, v)
	if host == "" {
		return ""
	}
	scheme := "ws"
	if h.prod {
		scheme = "wss"
	}
	return fmt.Sprintf("%s://%s/", scheme, host)
}

type consoleTokenDTO struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	ExpiresIn int `json:"expires_in"`
}

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
