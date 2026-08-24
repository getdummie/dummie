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

// Admin management of the federated sign-in providers. The flow itself is in
// oidc.go; this file is only the record an admin keeps about one.

// oidcSlugPlaceholder stands in for the slug in the redirect URI the templates
// endpoint hands out. Chosen to survive URL path escaping unchanged, so what the
// client substitutes into is what it was given.
const oidcSlugPlaceholder = "SLUG"

// oidcProviderDTO is one provider as the admin screen sees it.
//
// The client secret is never in here. It is writable and it is used server-side,
// but a screen that renders a credential turns every open tab into somewhere it
// can leak from -- the same reason the settings table's secrets read back empty.
// SecretSet is what the UI shows instead.
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
	// RedirectURI is computed, not stored: it is what the provider has to have
	// registered, and an admin should be able to copy it rather than assemble it
	// out of the control URL and the slug by hand.
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

// oidcTemplate is a starting point for a provider an operator is likely to be
// adding. It fills the form; nothing about it is stored, and a provider created
// from one is an ordinary row afterwards.
type oidcTemplate struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Issuer      string `json:"issuer"`
	Scopes      string `json:"scopes"`
	// Hint is the part an operator cannot be told by a default: where to get the
	// client id and secret, and what to put in the provider's own configuration.
	Hint string `json:"hint"`
	// IssuerEditable is false where the issuer is a fixed, well-known address, so
	// the UI can present it as a fact rather than as something to fill in.
	IssuerEditable bool `json:"issuer_editable"`
	// DocsURL is where the client id and secret are created. Part of the template
	// because finding it is most of the work of adding a provider.
	DocsURL string `json:"docs_url,omitempty"`
	// Steps is what to do at the provider's end, in order. A list rather than a
	// paragraph because it is followed while switching between two consoles.
	Steps []string `json:"steps,omitempty"`
}

// oidcTemplates is one entry per provider worth pre-filling. Google only for
// now; the shape is general, so another is a literal here and nothing else.
// Anything not listed is added through the same form with the fields left blank,
// which is all a template saves anyone from.
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

// ListOIDCTemplates hands the UI the starting points. Static, but served rather
// than duplicated in the client so the two cannot drift.
// The redirect URI is the one thing an operator needs *before* saving -- it goes
// into the provider's console, and the provider has to accept it before a sign-in
// can be tested. So the pattern comes back with the templates and the form fills
// the slug in as it is typed, rather than the URI only appearing afterwards. The
// path is built here so the client is not a second place that knows it.
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

// oidcProviderReq is both the create and the update body.
//
// ClientSecret is empty-means-unchanged on update, because the field starts
// empty on a screen that never shows the stored one. Clearing it -- turning the
// provider into a public client that authenticates with PKCE alone -- therefore
// needs to be said, which is what ClearSecret is for.
type oidcProviderReq struct {
	Slug         string `json:"slug"`
	DisplayName  string `json:"display_name"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	ClearSecret  bool   `json:"clear_secret"`
	Scopes       string `json:"scopes"`
	// Pointers so that omitting them means "leave as is" on an update rather than
	// "set to false" -- the enable switch in the list sends a partial body, and a
	// plain bool there would silently close sign-ups on every provider it touched.
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

// UpdateOIDCProvider rewrites everything except the slug, which is part of the
// redirect URI the provider has registered: changing it here would silently
// break sign-in at the far end, so it is fixed once created.
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

	// A discovery document is cached by issuer, so an admin who has just corrected
	// one has to see the new one used on the next attempt rather than after a
	// restart. Both are dropped: the old entry is now unreferenced, and the new
	// one may have been cached from a failed earlier attempt at a different row.
	forgetOIDCIssuer(existing.Issuer)
	forgetOIDCIssuer(p.Issuer)
	return c.JSON(http.StatusOK, h.toOIDCProviderDTO(p))
}

// DeleteOIDCProvider removes the provider and, by cascade, the links to it. The
// accounts those links pointed at are left alone: a person who signed in through
// a provider that has been withdrawn still has an account, and deleting it here
// would take their VMs with it.
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

// --- validation -------------------------------------------------------------

// boolOr resolves an optional body field: the value if the caller sent one, and
// what is already there otherwise.
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

// normalizeOIDCReq trims and checks the fields shared by create and update, and
// returns them cleaned. Everything here is pasted out of another console, where
// a trailing space or a copied trailing slash is never meant and would otherwise
// fail somewhere far less obvious -- an issuer that does not match the one in the
// identity token is rejected by the verifier with nothing to point at.
func (h *AdminHandler) normalizeOIDCReq(req *oidcProviderReq) (oidcProviderReq, error) {
	out := oidcProviderReq{
		Slug:        strings.ToLower(strings.TrimSpace(req.Slug)),
		DisplayName: strings.TrimSpace(req.DisplayName),
		Issuer:      strings.TrimRight(strings.TrimSpace(req.Issuer), "/"),
		ClientID:    strings.TrimSpace(req.ClientID),
		Scopes:      strings.Join(parseScopes(req.Scopes), " "),
	}
	// An empty field means the caller has no opinion, and parseScopes would leave
	// just "openid" -- enough to authenticate, but not to learn the email this
	// server matches an account on.
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

// validateOIDCIssuer holds the issuer to what the specification says it is: the
// exact origin the provider puts in the "iss" claim, which is where its
// discovery document is found.
func (h *AdminHandler) validateOIDCIssuer(issuer string) error {
	if issuer == "" {
		return errors.New("an issuer URL is required")
	}
	u, err := url.Parse(issuer)
	if err != nil || u.Host == "" {
		return errors.New("issuer must be a URL like https://accounts.google.com")
	}
	// http is a downgrade that puts the whole exchange -- codes, tokens, the
	// client secret -- on the wire in the clear. Allowed on a dev box, where the
	// provider is often a container on localhost, and nowhere else.
	if u.Scheme != "https" && !(u.Scheme == "http" && !h.cfg.prod) {
		return errors.New("issuer must be an https URL")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("issuer must be a bare URL with no query or fragment")
	}
	return nil
}
