package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

const proxyDesktopAudPrefix = "desktop:"

// The port a native client connects to. dproxy owns it on every fleet host.
const rdpPublicPort = 3389

func (h *UserHandler) desktopHost(ctx context.Context, v db.Vm) string {
	tld := h.vmDomainTLD(ctx, v)
	if tld == "" {
		return ""
	}
	return fmt.Sprintf("%s.%s.%s", v.Name, proxyDesktopLabel, tld)
}

func (h *UserHandler) desktopURL(ctx context.Context, v db.Vm) string {
	host := h.desktopHost(ctx, v)
	if host == "" {
		return ""
	}
	scheme := "ws"
	if h.prod {
		scheme = "wss"
	}
	return fmt.Sprintf("%s://%s/", scheme, host)
}

// rdpGateway is the host:port a native client dials. The proxy listens on the
// fleet host itself, which is the same name the VM's own hostname sits under.
func (h *UserHandler) rdpGateway(ctx context.Context, v db.Vm) string {
	tld := h.vmDomainTLD(ctx, v)
	if tld == "" {
		return ""
	}
	return fmt.Sprintf("%s.%s", v.Name, tld)
}

type rdpCredentialsDTO struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// vmForOwnerRunning is the ownership and liveness check every remote-desktop
// endpoint shares. The console deliberately does not make the running check, and
// the result is a token that mints fine and then fails at connect time; there is
// no reason to repeat that here.
func (h *UserHandler) vmForOwnerRunning(c *echo.Context) (db.Vm, error) {
	var zero db.Vm
	owner, err := callerID(c)
	if err != nil {
		return zero, echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	if h.proxy.secret == "" {
		return zero, echo.NewHTTPError(http.StatusServiceUnavailable, "remote desktop is not configured on this installation")
	}
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return zero, echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
	}
	v, err := h.q.GetVMForOwner(c.Request().Context(), db.GetVMForOwnerParams{ID: pgID, CreatedBy: owner})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, echo.NewHTTPError(http.StatusNotFound, "no such vm")
		}
		return zero, echo.NewHTTPError(http.StatusInternalServerError, "could not read vm")
	}
	if v.Status != "running" {
		return zero, echo.NewHTTPError(http.StatusConflict, "this vm is "+v.Status+", so it has no desktop")
	}
	return v, nil
}

func (h *UserHandler) rdpCredentials(ctx context.Context, v db.Vm) (rdpCredentialsDTO, error) {
	host := h.rdpGateway(ctx, v)
	if host == "" {
		return rdpCredentialsDTO{}, echo.NewHTTPError(http.StatusConflict,
			"the host this vm runs on has no domain, so it has no remote desktop")
	}
	vmID := uuid.UUID(v.ID.Bytes).String()
	return rdpCredentialsDTO{
		Host:     host,
		Port:     rdpPublicPort,
		Username: v.Name,
		Password: rdpPassword(h.proxy.secret, vmID, v.RdpNonce),
	}, nil
}

// @Summary     Read this VM's remote desktop credentials
// @Description Returns the gateway address and the username and password a native RDP client authenticates to the proxy with. The username is the VM name, which is what selects the VM; the password is issued by this server and is not the VM's own. The proxy verifies both and then logs into the guest itself, so these credentials reach nothing but this one VM.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     200 {object} rdpCredentialsDTO
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError "the vm is not running, or its host has no domain"
// @Failure     503 {object} apiError "remote desktop is not configured on this installation"
// @Router      /vms/{id}/rdp-credentials [get]
func (h *UserHandler) RDPCredentials(c *echo.Context) error {
	v, err := h.vmForOwnerRunning(c)
	if err != nil {
		return err
	}
	creds, err := h.rdpCredentials(c.Request().Context(), v)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, creds)
}

// @Summary     Download an .rdp file for this VM
// @Description Returns a connection file carrying the gateway address and username. The password is deliberately not embedded: the file is a download that lands in a shared folder on most systems, and the client prompts for it once and can remember it itself.
// @Tags        vms
// @Produce     plain
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     200 {string} string "an rdp connection file"
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError
// @Failure     503 {object} apiError
// @Router      /vms/{id}/rdp-file [get]
func (h *UserHandler) RDPFile(c *echo.Context) error {
	v, err := h.vmForOwnerRunning(c)
	if err != nil {
		return err
	}
	creds, err := h.rdpCredentials(c.Request().Context(), v)
	if err != nil {
		return err
	}

	// CRLF throughout: mstsc rejects a file with bare newlines.
	var b strings.Builder
	for _, line := range []string{
		fmt.Sprintf("full address:s:%s:%d", creds.Host, creds.Port),
		fmt.Sprintf("username:s:%s", creds.Username),
		// 2 == warn on a certificate the client cannot verify, but let the user
		// connect. A fleet with no certificate of its own falls back to a
		// self-signed one, and "do not connect" would make that unusable.
		"authentication level:i:2",
		// Always ask. The password is issued by this server and can be rotated,
		// so a client reusing a saved one would fail with no way to correct it.
		"prompt for credentials:i:1",
		"negotiate security layer:i:1",
		"screen mode id:i:2",
		"session bpp:i:32",
		"redirectclipboard:i:1",
		"audiomode:i:0",
	} {
		b.WriteString(line)
		b.WriteString("\r\n")
	}

	c.Response().Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", v.Name+".rdp"))
	return c.Blob(http.StatusOK, "application/x-rdp", []byte(b.String()))
}

// @Summary     Rotate this VM's remote desktop password
// @Description Replaces the nonce the password is derived from and republishes the proxy config, so the previous password stops working as soon as the host picks the change up. Returns the new credentials.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     200 {object} rdpCredentialsDTO
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError
// @Failure     503 {object} apiError
// @Router      /vms/{id}/rdp-rotate [post]
func (h *UserHandler) RDPRotate(c *echo.Context) error {
	v, err := h.vmForOwnerRunning(c)
	if err != nil {
		return err
	}
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}

	ctx := c.Request().Context()
	rotated, err := h.q.RotateVMRDPNonceForOwner(ctx, db.RotateVMRDPNonceForOwnerParams{
		ID: v.ID, CreatedBy: owner,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "no such vm")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not rotate the password")
	}

	creds, err := h.rdpCredentials(ctx, rotated)
	if err != nil {
		return err
	}
	pushProxyConfig(ctx, h.q, h.hub, h.proxy, rotated.ClientID)
	return c.JSON(http.StatusOK, creds)
}

type desktopTokenDTO struct {
	URL       string `json:"url"`
	Token     string `json:"token"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	ExpiresIn int    `json:"expires_in"`
}

// @Summary     Mint a browser desktop token
// @Description Returns the websocket url for this VM's desktop, a short-lived token that opens it, and the guest credentials the in-browser client authenticates with. POST rather than GET for the same reason as the console token: it is a credential, and a GET would leave it in history, referrers and request logs.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     200 {object} desktopTokenDTO
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError
// @Failure     503 {object} apiError
// @Router      /vms/{id}/desktop-token [post]
func (h *UserHandler) DesktopToken(c *echo.Context) error {
	v, err := h.vmForOwnerRunning(c)
	if err != nil {
		return err
	}
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}

	ctx := c.Request().Context()
	host := h.desktopHost(ctx, v)
	if host == "" {
		return echo.NewHTTPError(http.StatusConflict, "the host this vm runs on has no domain, so it has no desktop")
	}
	u, err := h.q.GetUserByID(ctx, owner)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the caller")
	}

	token, err := mintProxyToken(h.proxy.secret, u.Email, proxyDesktopAudPrefix+host, time.Now())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not mint token")
	}
	// The browser client speaks RDP itself and so needs the guest's credentials.
	// It only ever reaches a guest the token already authorized it for.
	return c.JSON(http.StatusOK, desktopTokenDTO{
		URL:       h.desktopURL(ctx, v),
		Token:     token,
		Username:  rdpGuestUser,
		Password:  rdpGuestPass,
		ExpiresIn: int(proxyTokenTTL.Seconds()),
	})
}
