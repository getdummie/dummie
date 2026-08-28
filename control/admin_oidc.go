package main

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

const oidcSlugPlaceholder = "SLUG"

type oidcProviderDTO struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Issuer      string `json:"issuer"`
	ClientID    string `json:"client_id"`
	SecretSet   bool   `json:"secret_set"`
	Scopes      string `json:"scopes"`
	Enabled     bool   `json:"enabled"`
	AllowSignup bool   `json:"allow_signup"`
	RedirectURI string `json:"redirect_uri"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func (h *AdminHandler) toOIDCProviderDTO(p db.OidcProvider) oidcProviderDTO {
	return oidcProviderDTO{
		ID:          uuid.UUID(p.ID.Bytes).String(),
		Slug:        p.Slug,
		DisplayName: p.DisplayName,
		Issuer:      p.Issuer,
		ClientID:    p.ClientID,
		SecretSet:   p.ClientSecret != "",
		Scopes:      p.Scopes,
		Enabled:     p.Enabled,
		AllowSignup: p.AllowSignup,
		RedirectURI: oidcRedirectURI(h.controlURL, p.Slug),
		CreatedAt:   p.CreatedAt.Time.Format(time.RFC3339),
		UpdatedAt:   p.UpdatedAt.Time.Format(time.RFC3339),
	}
}

type oidcTemplate struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Issuer      string `json:"issuer"`
	Scopes      string `json:"scopes"`
	Hint string `json:"hint"`
	IssuerEditable bool `json:"issuer_editable"`
	DocsURL string `json:"docs_url,omitempty"`
	Steps []string `json:"steps,omitempty"`
}

var oidcTemplates = []oidcTemplate{
	{
		ID:             "google",
		Label:          "Google",
		Slug:           "google",
		DisplayName:    "Google",
		Issuer:         "https://accounts.google.com",
		Scopes:         "openid profile email",
		IssuerEditable: false,
		DocsURL:        "https://console.cloud.google.com/apis/credentials",
		Hint:           "Google always asserts whether the email it returns is verified, which is what an account here is matched on.",
		Steps: []string{
			"In the Google Cloud console, open APIs & Services > Credentials.",
			"Create Credentials > OAuth client ID, application type \"Web application\".",
			"Paste the redirect URI shown below into Authorised redirect URIs.",
			"Copy the client ID and client secret it gives you into the fields here.",
		},
	},
}

func (h *AdminHandler) ListOIDCTemplates(c *echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{
		"items":                 oidcTemplates,
		"redirect_uri_pattern":  oidcRedirectURI(h.controlURL, oidcSlugPlaceholder),
		"redirect_uri_slug_key": oidcSlugPlaceholder,
	})
}

func (h *AdminHandler) ListOIDCProviders(c *echo.Context) error {
	rows, err := h.q.ListOIDCProviders(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list providers")
	}
	items := make([]oidcProviderDTO, 0, len(rows))
	for _, p := range rows {
		items = append(items, h.toOIDCProviderDTO(p))
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

type oidcProviderReq struct {
	Slug         string `json:"slug"`
	DisplayName  string `json:"display_name"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	ClearSecret  bool   `json:"clear_secret"`
	Scopes       string `json:"scopes"`
	Enabled     *bool `json:"enabled"`
	AllowSignup *bool `json:"allow_signup"`
}

func (h *AdminHandler) CreateOIDCProvider(c *echo.Context) error {
	var req oidcProviderReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	clean, err := h.normalizeOIDCReq(&req)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if err := validateOIDCSlug(clean.Slug); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	p, err := h.q.CreateOIDCProvider(c.Request().Context(), db.CreateOIDCProviderParams{
		Slug:         clean.Slug,
		DisplayName:  clean.DisplayName,
		Issuer:       clean.Issuer,
		ClientID:     clean.ClientID,
		ClientSecret: req.ClientSecret,
		Scopes:       clean.Scopes,
		Enabled:      boolOr(req.Enabled, true),
		AllowSignup:  boolOr(req.AllowSignup, true),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return echo.NewHTTPError(http.StatusConflict, "a provider with that slug already exists")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not create provider")
	}
	return c.JSON(http.StatusCreated, h.toOIDCProviderDTO(p))
}

func (h *AdminHandler) UpdateOIDCProvider(c *echo.Context) error {
	ctx := c.Request().Context()
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid provider id")
	}
	existing, err := h.q.GetOIDCProvider(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "provider not found")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not load provider")
	}

	var req oidcProviderReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	clean, err := h.normalizeOIDCReq(&req)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	p, err := h.q.UpdateOIDCProvider(ctx, db.UpdateOIDCProviderParams{
		ID:           id,
		DisplayName:  clean.DisplayName,
		Issuer:       clean.Issuer,
		ClientID:     clean.ClientID,
		ClientSecret: req.ClientSecret,
		Scopes:       clean.Scopes,
		Enabled:      boolOr(req.Enabled, existing.Enabled),
		AllowSignup:  boolOr(req.AllowSignup, existing.AllowSignup),
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save provider")
	}
	if req.ClearSecret && req.ClientSecret == "" {
		if p, err = h.q.ClearOIDCProviderSecret(ctx, id); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "could not clear the client secret")
		}
	}

	forgetOIDCIssuer(existing.Issuer)
	forgetOIDCIssuer(p.Issuer)
	return c.JSON(http.StatusOK, h.toOIDCProviderDTO(p))
}

func (h *AdminHandler) DeleteOIDCProvider(c *echo.Context) error {
	ctx := c.Request().Context()
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid provider id")
	}
	existing, err := h.q.GetOIDCProvider(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "provider not found")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not load provider")
	}
	if err := h.q.DeleteOIDCProvider(ctx, id); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete provider")
	}
	forgetOIDCIssuer(existing.Issuer)
	return c.NoContent(http.StatusNoContent)
}

func boolOr(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

var oidcSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func validateOIDCSlug(slug string) error {
	if !oidcSlugRe.MatchString(slug) {
		return errors.New("slug must be lowercase letters, numbers and dashes, starting with a letter or number")
	}
	return nil
}

func (h *AdminHandler) normalizeOIDCReq(req *oidcProviderReq) (oidcProviderReq, error) {
	out := oidcProviderReq{
		Slug:        strings.ToLower(strings.TrimSpace(req.Slug)),
		DisplayName: strings.TrimSpace(req.DisplayName),
		Issuer:      strings.TrimRight(strings.TrimSpace(req.Issuer), "/"),
		ClientID:    strings.TrimSpace(req.ClientID),
		Scopes:      strings.Join(parseScopes(req.Scopes), " "),
	}
	if strings.TrimSpace(req.Scopes) == "" {
		out.Scopes = "openid profile email"
	}
	req.ClientSecret = strings.TrimSpace(req.ClientSecret)

	if out.DisplayName == "" {
		return out, errors.New("a display name is required; it is what the sign-in button says")
	}
	if out.ClientID == "" {
		return out, errors.New("a client id is required")
	}
	if err := h.validateOIDCIssuer(out.Issuer); err != nil {
		return out, err
	}
	return out, nil
}

func (h *AdminHandler) validateOIDCIssuer(issuer string) error {
	if issuer == "" {
		return errors.New("an issuer URL is required")
	}
	u, err := url.Parse(issuer)
	if err != nil || u.Host == "" {
		return errors.New("issuer must be a URL like https://accounts.google.com")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && !h.cfg.prod) {
		return errors.New("issuer must be an https URL")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("issuer must be a bare URL with no query or fragment")
	}
	return nil
}
