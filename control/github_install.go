package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

const githubInstallStateTTL = 15 * time.Minute

// Install sends the browser to github to install the app, remembering which
// integration the resulting installation belongs to. The state lives in the
// database rather than a cookie: it decides an ownership binding, so it must
// not be something the browser can rewrite.
//
// @Summary     Start a github app install for an integration
// @Tags        integrations
// @Router      /integrations/{id}/github/install [get]
func (h *IntegrationHandler) Install(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return err
	}
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid integration id")
	}

	ctx := c.Request().Context()
	if _, err := h.q.GetIntegrationForOwner(ctx, db.GetIntegrationForOwnerParams{ID: id, OwnerID: owner}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "no such integration")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the integration")
	}

	app, err := h.q.GetGitHubApp(ctx)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && app.Slug == "") {
		return echo.NewHTTPError(http.StatusServiceUnavailable,
			"no github app is configured on this control server yet")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the github app")
	}

	state, err := newRefreshToken()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not start the install")
	}
	if err := h.q.CreateGitHubInstallState(ctx, db.CreateGitHubInstallStateParams{
		StateHash:     hashRefresh(state),
		OwnerID:       owner,
		IntegrationID: id,
	}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not start the install")
	}
	_ = h.q.DeleteExpiredGitHubInstallStates(ctx, githubInstallStateTTL.Seconds())

	target := "https://github.com/apps/" + url.PathEscape(app.Slug) + "/installations/new?state=" + url.QueryEscape(state)
	return c.JSON(http.StatusOK, map[string]string{"url": target})
}

// Manage points the browser at the installation's own page on github, where the
// repositories granted to it are changed. Which page that is depends on whether
// the app sits on a user or an organisation, which only github knows.
//
// @Summary     Where to change the repositories granted to an installation
// @Tags        integrations
// @Produce     json
// @Router      /integrations/{id}/github/manage [get]
func (h *IntegrationHandler) Manage(c *echo.Context) error {
	owner, id, err := h.ownedIntegration(c)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	row, err := h.q.GetIntegrationForOwner(ctx, db.GetIntegrationForOwnerParams{ID: id, OwnerID: owner})
	if err != nil {
		return notFoundOr(err, "no such integration", "could not read the integration")
	}
	if !row.InstallationPk.Valid {
		return echo.NewHTTPError(http.StatusConflict, "connect a github account to this integration first")
	}

	app, err := h.githubApp(ctx)
	if err != nil {
		return err
	}
	account, err := app.getInstallation(ctx, row.InstallationID.Int64)
	if err != nil {
		log.Printf("could not read github installation %d: %v", row.InstallationID.Int64, err)
		return echo.NewHTTPError(http.StatusBadGateway, "github could not be asked about this installation")
	}

	installation := strconv.FormatInt(row.InstallationID.Int64, 10)
	target := "https://github.com/settings/installations/" + installation
	if !strings.EqualFold(account.AccountType, "User") {
		target = "https://github.com/organizations/" + url.PathEscape(account.AccountLogin) +
			"/settings/installations/" + installation
	}
	return c.JSON(http.StatusOK, map[string]string{"url": target})
}

// Callback is authenticated by the state alone, not by a session: github sends
// the browser here as a top-level redirect, which carries no access token.
//
// @Summary     Finish a github app install
// @Tags        integrations
// @Router      /integrations/github/callback [get]
func (h *IntegrationHandler) Callback(c *echo.Context) error {
	ctx := c.Request().Context()
	state := c.QueryParam("state")
	if state == "" {
		// "Redirect on update" sends users here after they change an
		// installation's repositories, with no state because no install link
		// was involved. Nothing to record -- the repository list is read from
		// github on demand -- so this is a benign bounce, not a failure.
		if c.QueryParam("setup_action") == "update" {
			return h.installRedirect(c, h.integrationOfInstallation(ctx, c.QueryParam("installation_id")), "")
		}
		return h.installRedirect(c, "", "missing_state")
	}

	row, err := h.q.TakeGitHubInstallState(ctx, db.TakeGitHubInstallStateParams{
		StateHash:     hashRefresh(state),
		WithinSeconds: githubInstallStateTTL.Seconds(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return h.installRedirect(c, "", "expired_state")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the install state")
	}
	integrationID := domainIDString(row.IntegrationID)

	// The org admin has to approve the install before it exists, so there is
	// nothing to record yet.
	if c.QueryParam("setup_action") == "request" {
		return h.installRedirect(c, integrationID, "awaiting_approval")
	}

	installationID, err := strconv.ParseInt(strings.TrimSpace(c.QueryParam("installation_id")), 10, 64)
	if err != nil || installationID <= 0 {
		return h.installRedirect(c, integrationID, "missing_installation")
	}

	app, err := h.q.GetGitHubApp(ctx)
	if err != nil {
		return h.installRedirect(c, integrationID, "no_app")
	}
	client, err := parseGitHubApp(app.AppID, app.PrivateKey)
	if err != nil {
		log.Printf("the configured github app is unusable: %v", err)
		return h.installRedirect(c, integrationID, "no_app")
	}

	account, err := client.getInstallation(ctx, installationID)
	if err != nil {
		log.Printf("could not read github installation %d: %v", installationID, err)
		return h.installRedirect(c, integrationID, "github_unreachable")
	}

	inst, err := h.q.UpsertGitHubInstallation(ctx, db.UpsertGitHubInstallationParams{
		AppPk:               app.ID,
		InstallationID:      installationID,
		AccountLogin:        account.AccountLogin,
		AccountType:         account.AccountType,
		RepositorySelection: githubRepositorySelection(account.RepositorySelection),
		InstalledBy:         row.OwnerID,
	})
	if err != nil {
		log.Printf("could not record github installation %d: %v", installationID, err)
		return h.installRedirect(c, integrationID, "save_failed")
	}

	if err := h.q.SetIntegrationInstallation(ctx, db.SetIntegrationInstallationParams{
		ID:             row.IntegrationID,
		InstallationPk: inst.ID,
	}); err != nil {
		log.Printf("could not attach installation %d to integration %s: %v", installationID, integrationID, err)
		return h.installRedirect(c, integrationID, "save_failed")
	}

	log.Printf("github installation %d (%s) connected to integration %s",
		installationID, account.AccountLogin, integrationID)
	return h.installRedirect(c, integrationID, "")
}

// integrationOfInstallation names the page to land on after github's stateless
// update redirect. It stays empty unless exactly one integration uses the
// installation: with several there is no way to tell which one was being
// edited, and the caller is unauthenticated here so guessing is not free.
// Landing on a page you do not own is harmless -- every read behind it is
// checked against the session -- but landing on the wrong one is confusing.
func (h *IntegrationHandler) integrationOfInstallation(ctx context.Context, raw string) string {
	installationID, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || installationID <= 0 {
		return ""
	}
	ids, err := h.q.ListIntegrationIDsByInstallationID(ctx, installationID)
	if err != nil || len(ids) != 1 {
		return ""
	}
	return domainIDString(ids[0])
}

func githubRepositorySelection(v string) string {
	if v == "all" {
		return "all"
	}
	return "selected"
}

func (h *IntegrationHandler) installRedirect(c *echo.Context, integrationID, reason string) error {
	dest := strings.TrimRight(h.controlURL, "/") + "/integrations"
	if integrationID != "" {
		dest += "/" + integrationID
	}
	if reason != "" {
		dest += "?error=" + url.QueryEscape(reason)
	}
	return c.Redirect(http.StatusFound, dest)
}
