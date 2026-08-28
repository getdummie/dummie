package main

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"
	"golang.org/x/oauth2"

	"control/internal/db"
)

const (
	oidcFlowCookie = "oidc_flow"
	oidcFlowTTL    = 10 * time.Minute

	oidcDefaultDest = "/dashboard"
	oidcErrorDest = "/signin"
)

var oidcHTTPClient = &http.Client{Timeout: 15 * time.Second}

var oidcDiscoveryCtx = oidc.ClientContext(context.Background(), oidcHTTPClient)

var oidcProviderCache sync.Map

func discoverOIDC(issuer string) (*oidc.Provider, error) {
	if p, ok := oidcProviderCache.Load(issuer); ok {
		return p.(*oidc.Provider), nil
	}
	p, err := oidc.NewProvider(oidcDiscoveryCtx, issuer)
	if err != nil {
		return nil, err
	}
	oidcProviderCache.Store(issuer, p)
	return p, nil
}

func forgetOIDCIssuer(issuer string) {
	oidcProviderCache.Delete(issuer)
}

type OIDCHandler struct {
	q    *db.Queries
	auth *AuthHandler
	controlURL string
}

func oidcRedirectURI(controlURL, slug string) string {
	return strings.TrimRight(controlURL, "/") + "/api/v1/oidc/" + url.PathEscape(slug) + "/callback"
}

type oidcFlow struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	Slug string `json:"slug"`
	Dest string `json:"dest"`
}

func (h *OIDCHandler) setFlowCookie(c *echo.Context, f oidcFlow) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	http.SetCookie(c.Response(), &http.Cookie{
		Name:  oidcFlowCookie,
		Value: base64.RawURLEncoding.EncodeToString(b),
		Path:  "/",
		HttpOnly: true, Secure: h.auth.cfg.prod, SameSite: http.SameSiteLaxMode,
		MaxAge: int(oidcFlowTTL.Seconds()),
	})
	return nil
}

func (h *OIDCHandler) takeFlowCookie(c *echo.Context) (oidcFlow, bool) {
	http.SetCookie(c.Response(), &http.Cookie{
		Name: oidcFlowCookie, Value: "", Path: "/",
		HttpOnly: true, Secure: h.auth.cfg.prod, SameSite: http.SameSiteLaxMode,
		MaxAge: -1,
	})
	ck, err := c.Request().Cookie(oidcFlowCookie)
	if err != nil {
		return oidcFlow{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(ck.Value)
	if err != nil {
		return oidcFlow{}, false
	}
	var f oidcFlow
	if err := json.Unmarshal(raw, &f); err != nil {
		return oidcFlow{}, false
	}
	return f, f.State != ""
}

type oidcProviderPublicDTO struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	StartURL    string `json:"start_url"`
}

// @Summary     Sign-in providers on offer
// @Tags        auth
// @Produce     json
// @Success     200 {object} map[string][]oidcProviderPublicDTO
// @Router      /oidc/providers [get]
func (h *OIDCHandler) ListPublicProviders(c *echo.Context) error {
	rows, err := h.q.ListEnabledOIDCProviders(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list providers")
	}
	items := make([]oidcProviderPublicDTO, 0, len(rows))
	for _, p := range rows {
		items = append(items, oidcProviderPublicDTO{
			Slug:        p.Slug,
			DisplayName: p.DisplayName,
			StartURL:    "/api/v1/oidc/" + url.PathEscape(p.Slug) + "/start",
		})
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// @Summary     Begin a federated sign-in
// @Tags        auth
// @Param       slug     path  string true  "provider slug"
// @Param       redirect query string false "same-origin path to land on afterwards"
// @Success     302
// @Router      /oidc/{slug}/start [get]
func (h *OIDCHandler) Start(c *echo.Context) error {
	ctx := c.Request().Context()
	p, err := h.q.GetOIDCProviderBySlug(ctx, c.Param("slug"))
	if err != nil || !p.Enabled {
		return h.fail(c, "that sign-in provider is not available")
	}

	prov, err := discoverOIDC(p.Issuer)
	if err != nil {
		log.Printf("oidc: discovery failed for %s (%s): %v", p.Slug, p.Issuer, err)
		return h.fail(c, "could not reach the sign-in provider")
	}

	state, err := randomToken()
	if err != nil {
		return h.fail(c, "could not start sign-in")
	}
	nonce, err := randomToken()
	if err != nil {
		return h.fail(c, "could not start sign-in")
	}
	verifier := oauth2.GenerateVerifier()

	flow := oidcFlow{
		State: state, Nonce: nonce, Verifier: verifier, Slug: p.Slug,
		Dest: sameOriginPath(c.Request().URL.Query().Get("redirect"), oidcDefaultDest),
	}
	if err := h.setFlowCookie(c, flow); err != nil {
		return h.fail(c, "could not start sign-in")
	}

	conf := h.oauthConfig(p, prov)
	authURL := conf.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	http.Redirect(c.Response(), c.Request(), authURL, http.StatusFound)
	return nil
}

// @Summary     Complete a federated sign-in
// @Tags        auth
// @Param       slug path string true "provider slug"
// @Success     302
// @Router      /oidc/{slug}/callback [get]
func (h *OIDCHandler) Callback(c *echo.Context) error {
	ctx := oidc.ClientContext(c.Request().Context(), oidcHTTPClient)
	q := c.Request().URL.Query()

	flow, ok := h.takeFlowCookie(c)
	if !ok {
		return h.fail(c, "the sign-in attempt expired; please try again")
	}
	if subtle.ConstantTimeCompare([]byte(flow.State), []byte(q.Get("state"))) != 1 {
		return h.fail(c, "the sign-in attempt could not be verified; please try again")
	}
	slug := c.Param("slug")
	if flow.Slug != slug {
		return h.fail(c, "the sign-in attempt could not be verified; please try again")
	}
	if e := q.Get("error"); e != "" {
		if desc := q.Get("error_description"); desc != "" {
			return h.fail(c, desc)
		}
		return h.fail(c, "the sign-in provider refused the request: "+e)
	}
	code := q.Get("code")
	if code == "" {
		return h.fail(c, "the sign-in provider returned no authorization code")
	}

	p, err := h.q.GetOIDCProviderBySlug(c.Request().Context(), slug)
	if err != nil || !p.Enabled {
		return h.fail(c, "that sign-in provider is not available")
	}
	prov, err := discoverOIDC(p.Issuer)
	if err != nil {
		log.Printf("oidc: discovery failed for %s (%s): %v", p.Slug, p.Issuer, err)
		return h.fail(c, "could not reach the sign-in provider")
	}

	conf := h.oauthConfig(p, prov)
	tok, err := conf.Exchange(ctx, code, oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		log.Printf("oidc: code exchange failed for %s: %v", p.Slug, err)
		return h.fail(c, "the sign-in provider rejected the authorization code")
	}

	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return h.fail(c, "the sign-in provider returned no identity token")
	}
	idToken, err := prov.Verifier(&oidc.Config{ClientID: p.ClientID}).Verify(ctx, rawID)
	if err != nil {
		log.Printf("oidc: id token verification failed for %s: %v", p.Slug, err)
		return h.fail(c, "the identity token could not be verified")
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(flow.Nonce)) != 1 {
		return h.fail(c, "the identity token did not match this sign-in attempt")
	}

	var cl oidcClaims
	if err := idToken.Claims(&cl); err != nil {
		return h.fail(c, "the identity token could not be read")
	}
	cl.Subject = idToken.Subject
	if cl.Email == "" {
		if ui, err := prov.UserInfo(ctx, oauth2.StaticTokenSource(tok)); err == nil {
			var extra oidcClaims
			if err := ui.Claims(&extra); err == nil {
				cl.merge(extra)
			}
		}
	}
	if cl.Subject == "" {
		return h.fail(c, "the sign-in provider did not identify the account")
	}

	u, err := h.resolveUser(c.Request().Context(), p, cl)
	if err != nil {
		return h.fail(c, err.Error())
	}

	if _, err := h.auth.startSession(c, u); err != nil {
		return h.fail(c, "could not start a session")
	}
	http.Redirect(c.Response(), c.Request(), flow.Dest, http.StatusFound)
	return nil
}

func (h *OIDCHandler) oauthConfig(p db.OidcProvider, prov *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     p.ClientID,
		ClientSecret: p.ClientSecret,
		Endpoint:     prov.Endpoint(),
		RedirectURL:  oidcRedirectURI(h.controlURL, p.Slug),
		Scopes:       parseScopes(p.Scopes),
	}
}

func (h *OIDCHandler) fail(c *echo.Context, msg string) error {
	http.Redirect(c.Response(), c.Request(),
		oidcErrorDest+"?oidc_error="+url.QueryEscape(msg), http.StatusFound)
	return nil
}

type oidcClaims struct {
	Subject           string   `json:"sub"`
	Email             string   `json:"email"`
	EmailVerified     flexBool `json:"email_verified"`
	Name              string   `json:"name"`
	GivenName         string   `json:"given_name"`
	FamilyName        string   `json:"family_name"`
	PreferredUsername string   `json:"preferred_username"`
}

func (c *oidcClaims) merge(o oidcClaims) {
	if c.Email == "" {
		c.Email = o.Email
		c.EmailVerified = o.EmailVerified
	}
	if c.Name == "" {
		c.Name = o.Name
	}
	if c.GivenName == "" {
		c.GivenName = o.GivenName
	}
	if c.FamilyName == "" {
		c.FamilyName = o.FamilyName
	}
	if c.PreferredUsername == "" {
		c.PreferredUsername = o.PreferredUsername
	}
}

type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch t := v.(type) {
	case bool:
		*b = flexBool(t)
	case string:
		*b = flexBool(strings.EqualFold(t, "true"))
	}
	return nil
}

func (h *OIDCHandler) resolveUser(ctx context.Context, p db.OidcProvider, cl oidcClaims) (db.User, error) {
	if id, err := h.q.GetOIDCIdentity(ctx, db.GetOIDCIdentityParams{ProviderID: p.ID, Subject: cl.Subject}); err == nil {
		u, err := h.q.GetUserByID(ctx, id.UserID)
		if err != nil {
			return db.User{}, errors.New("the account this identity belongs to no longer exists")
		}
		if err := h.q.TouchOIDCIdentity(ctx, db.TouchOIDCIdentityParams{
			ProviderID: p.ID, Subject: cl.Subject, Email: cl.Email,
		}); err != nil {
			log.Printf("oidc: could not record login for %s/%s: %v", p.Slug, cl.Subject, err)
		}
		return u, nil
	}

	email := strings.ToLower(strings.TrimSpace(cl.Email))
	if email == "" {
		return db.User{}, errors.New("the sign-in provider did not return an email address")
	}
	if !bool(cl.EmailVerified) {
		return db.User{}, errors.New("the sign-in provider has not verified this email address")
	}

	u, err := h.q.GetUserByEmail(ctx, email)
	switch {
	case err == nil:
	case errors.Is(err, pgx.ErrNoRows):
		if !p.AllowSignup {
			return db.User{}, errors.New("this provider cannot create new accounts; ask an administrator for one")
		}
		u, err = h.createFederatedUser(ctx, cl, email)
		if err != nil {
			return db.User{}, err
		}
	default:
		return db.User{}, errors.New("could not look up the account")
	}

	if _, err := h.q.LinkOIDCIdentity(ctx, db.LinkOIDCIdentityParams{
		ProviderID: p.ID, Subject: cl.Subject, UserID: u.ID, Email: email,
	}); err != nil {
		return db.User{}, errors.New("could not link the account")
	}
	return u, nil
}

func (h *OIDCHandler) createFederatedUser(ctx context.Context, cl oidcClaims, email string) (db.User, error) {
	userType := "user"
	if n, err := h.q.CountUsers(ctx); err == nil && n == 0 {
		userType = "admin"
	}
	first, last := cl.GivenName, cl.FamilyName
	if first == "" && cl.Name != "" {
		first, last = splitName(cl.Name)
	}

	base := usernameFromClaims(cl, email)
	for attempt := range 12 {
		candidate := base
		if attempt > 0 {
			candidate = fmt.Sprintf("%s%d", base, attempt+1)
		}
		u, err := h.q.CreateFederatedUser(ctx, db.CreateFederatedUserParams{
			Username: candidate, Email: email,
			FirstName: first, LastName: last, UserType: userType,
		})
		if err == nil {
			return u, nil
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
			return db.User{}, errors.New("could not create an account")
		}
		if u, err := h.q.GetUserByEmail(ctx, email); err == nil {
			return u, nil
		}
	}
	return db.User{}, errors.New("could not find a free username for this account")
}

var usernameStrip = regexp.MustCompile(`[^a-z0-9._-]+`)

func usernameFromClaims(cl oidcClaims, email string) string {
	candidate := cl.PreferredUsername
	if candidate == "" {
		candidate, _, _ = strings.Cut(email, "@")
	}
	candidate = usernameStrip.ReplaceAllString(strings.ToLower(candidate), "")
	candidate = strings.Trim(candidate, "._-")
	if len(candidate) > 24 {
		candidate = candidate[:24]
	}
	if candidate == "" {
		candidate = "user"
	}
	return candidate
}

func splitName(full string) (first, last string) {
	parts := strings.Fields(full)
	switch len(parts) {
	case 0:
		return "", ""
	case 1:
		return parts[0], ""
	default:
		return parts[0], strings.Join(parts[1:], " ")
	}
}

func parseScopes(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(fields)+1)
	seen := map[string]bool{}
	for _, f := range fields {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	if !seen[oidc.ScopeOpenID] {
		out = append([]string{oidc.ScopeOpenID}, out...)
	}
	return out
}

func sameOriginPath(raw, fallback string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return fallback
	}
	return raw
}
