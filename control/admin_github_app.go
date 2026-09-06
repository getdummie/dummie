package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

type githubAppDTO struct {
	AppID       int64  `json:"app_id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	KeySet      bool   `json:"key_set"`
	CallbackURL string `json:"callback_url"`
	UpdatedAt   string `json:"updated_at"`
}

func githubCallbackURL(controlURL string) string {
	return strings.TrimRight(controlURL, "/") + "/api/v1/integrations/github/callback"
}

func (h *AdminHandler) toGitHubAppDTO(a db.GithubApp) githubAppDTO {
	return githubAppDTO{
		AppID:       a.AppID,
		Slug:        a.Slug,
		Name:        a.Name,
		KeySet:      a.PrivateKey != "",
		CallbackURL: githubCallbackURL(h.controlURL),
		UpdatedAt:   a.UpdatedAt.Time.Format(time.RFC3339),
	}
}

// @Summary     The github app backing integrations
// @Tags        admin
// @Produce     json
// @Success     200 {object} githubAppDTO
// @Router      /admin/github-app [get]
func (h *AdminHandler) GetGitHubApp(c *echo.Context) error {
	app, err := h.q.GetGitHubApp(c.Request().Context())
	if errors.Is(err, pgx.ErrNoRows) {
		return c.JSON(http.StatusOK, githubAppDTO{CallbackURL: githubCallbackURL(h.controlURL)})
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the github app")
	}
	return c.JSON(http.StatusOK, h.toGitHubAppDTO(app))
}

type putGitHubAppReq struct {
	AppID      string `json:"app_id"`
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	PrivateKey string `json:"private_key"`
}

// @Summary     Configure the github app
// @Tags        admin
// @Accept      json
// @Produce     json
// @Success     200 {object} githubAppDTO
// @Router      /admin/github-app [put]
func (h *AdminHandler) PutGitHubApp(c *echo.Context) error {
	var req putGitHubAppReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	appID, err := strconv.ParseInt(strings.TrimSpace(req.AppID), 10, 64)
	if err != nil || appID <= 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "app_id must be the numeric app id from github")
	}

	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		return echo.NewHTTPError(http.StatusBadRequest,
			"slug is required; it is the name in the app's github url and is what the install link uses")
	}

	// Refuse a key that cannot sign before storing it, rather than discovering
	// it on the first clone a user attempts.
	key := strings.TrimSpace(req.PrivateKey)
	if key != "" {
		if _, err := parseGitHubApp(appID, key); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
	}

	ctx := c.Request().Context()

	// An empty key means "keep the stored one", so the first save has to carry
	// it. Checked before the upsert: afterwards the row would exist with no key
	// and every later save would preserve that.
	if key == "" {
		existing, err := h.q.GetGitHubApp(ctx)
		switch {
		case errors.Is(err, pgx.ErrNoRows), err == nil && existing.PrivateKey == "":
			return echo.NewHTTPError(http.StatusBadRequest,
				"a private key is required the first time the app is saved")
		case err != nil:
			return echo.NewHTTPError(http.StatusInternalServerError, "could not read the github app")
		case existing.AppID != appID:
			return echo.NewHTTPError(http.StatusBadRequest,
				"this is a different app id, so it needs its own private key")
		}
	}

	app, err := h.q.UpsertGitHubApp(ctx, db.UpsertGitHubAppParams{
		AppID:      appID,
		Slug:       slug,
		Name:       strings.TrimSpace(req.Name),
		PrivateKey: key,
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the github app")
	}
	return c.JSON(http.StatusOK, h.toGitHubAppDTO(app))
}

// @Summary     Forget the github app
// @Tags        admin
// @Success     204
// @Router      /admin/github-app [delete]
func (h *AdminHandler) DeleteGitHubApp(c *echo.Context) error {
	app, err := h.q.GetGitHubApp(c.Request().Context())
	if errors.Is(err, pgx.ErrNoRows) {
		return c.NoContent(http.StatusNoContent)
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the github app")
	}
	if err := h.q.DeleteGitHubApp(c.Request().Context(), app.ID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete the github app")
	}
	return c.NoContent(http.StatusNoContent)
}
