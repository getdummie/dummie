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

// Federated sign-in. A provider is a row an admin adds; everything about how to
// talk to it is discovered from its issuer, so this file knows the OIDC
// authorization-code flow and nothing about any particular vendor.
//
// The flow is code + PKCE + nonce, which is what both a confidential client (one
// with a secret) and a public one (PKCE alone) use today. The state, nonce and
// PKCE verifier live in one short-lived httpOnly cookie rather than a table: the
// value is only meaningful to the browser holding it, it is dead ten minutes
// later, and a row per abandoned sign-in attempt is a table that only ever grows.

const (
	// oidcFlowCookie carries the in-flight authorization request. Cleared on the
	// callback whether or not it succeeds, so a failed attempt does not leave a
	// stale flow that a later callback could complete.
	oidcFlowCookie = "oidc_flow"
	oidcFlowTTL    = 10 * time.Minute

	// oidcDefaultDest is where a federated sign-in lands when the flow did not
	// carry somewhere better.
	oidcDefaultDest = "/dashboard"
	// oidcErrorDest is the page that reports a failed sign-in. It is the sign-in
	// page because that is where a person who could not get in needs to be.
	oidcErrorDest = "/signin"
)

// oidcHTTPClient bounds every call this file makes out to a provider. These are
// made while a browser waits on a redirect, and a provider that accepts the
// connection and then says nothing would otherwise hold the request open until
// the server's own write timeout.
var oidcHTTPClient = &http.Client{Timeout: 15 * time.Second}

// oidcDiscoveryCtx is deliberately background-scoped. go-oidc keeps the context
// it discovers with and reuses it to re-fetch the provider's signing keys later,
// so handing it a request context would mean every key rotation after that
// request finished failed against a cancelled context.
var oidcDiscoveryCtx = oidc.ClientContext(context.Background(), oidcHTTPClient)

// oidcProviderCache holds discovered providers by issuer. Discovery is one HTTP
// round trip to a document that names endpoints and key locations, and those do
// not move; go-oidc refreshes the keys themselves on its own schedule.
//
// Keyed by issuer rather than by row id so that an admin correcting a typo in an
// issuer gets a fresh discovery rather than the old one under the same id.
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

// forgetOIDCIssuer drops a cached discovery so an admin who has just repointed a
// provider does not have to restart the server to see it take effect.
func forgetOIDCIssuer(issuer string) {
	oidcProviderCache.Delete(issuer)
}

// OIDCHandler serves both ends of the redirect dance. It borrows AuthHandler for
// the session it issues at the end: a federated sign-in produces exactly the
// same session as a password one, and two ways to mint one is two ways for the
// cookie flags to drift apart.
type OIDCHandler struct {
	q    *db.Queries
	auth *AuthHandler
	// controlURL is the origin the browser reaches this server on, which is what
	// the redirect URI has to be built from -- the provider redirects a browser,
	// not this process, so the address it binds is the wrong answer.
	controlURL string
}

// redirectURI is the callback as the provider must have it registered. Built in
// one place because it appears twice: here, in the authorization request, and on
// the admin screen, where it is the value an operator pastes into the provider.
func oidcRedirectURI(controlURL, slug string) string {
	return strings.TrimRight(controlURL, "/") + "/api/v1/oidc/" + url.PathEscape(slug) + "/callback"
}

// --- the flow cookie --------------------------------------------------------

type oidcFlow struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	// Slug binds the flow to the provider it was started for, so a code obtained
	// from one provider cannot be redeemed at another's callback.
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
		// Lax, not Strict: the provider sends the browser back here with a
		// top-level GET from its own origin, and Strict withholds the cookie on
		// exactly that navigation, which would fail every sign-in.
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

// --- handlers ---------------------------------------------------------------

type oidcProviderPublicDTO struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	StartURL    string `json:"start_url"`
}

// ListPublicProviders is what the sign-in page renders its buttons from.
//
// Public on purpose: it says only which providers this deployment accepts, which
// is the same thing the buttons themselves say, and the page needs it before
// anyone has a session.
//
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

// Start begins an authorization-code flow and redirects the browser to the
// provider.
//
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

// Callback completes the flow: it redeems the code, verifies the identity token,
// resolves it to an account, and lands the browser back in the app with the same
// session cookies a password sign-in would have set.
//
// Every failure here ends as a redirect rather than an error document, because
// the browser arrived by navigation and the person on the other end needs to be
// back on a page they can act on.
//
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
	// Constant time because the comparison is against a value the caller supplied
	// and the secret is the only thing standing between a planted code and a
	// session.
	if subtle.ConstantTimeCompare([]byte(flow.State), []byte(q.Get("state"))) != 1 {
		return h.fail(c, "the sign-in attempt could not be verified; please try again")
	}
	slug := c.Param("slug")
	if flow.Slug != slug {
		return h.fail(c, "the sign-in attempt could not be verified; please try again")
	}
	// The provider says no by redirecting back with an error, not by refusing the
	// redirect, so this is the ordinary "user pressed cancel" path.
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
	// Some providers keep email out of the identity token and only return it from
	// userinfo, so that is a second look rather than a failure.
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

// fail sends the browser back to the sign-in page carrying the reason. The
// message is put in a query parameter rather than a flash cookie because the SPA
// reads it on the page it lands on and nothing else needs it afterwards.
func (h *OIDCHandler) fail(c *echo.Context, msg string) error {
	http.Redirect(c.Response(), c.Request(),
		oidcErrorDest+"?oidc_error="+url.QueryEscape(msg), http.StatusFound)
	return nil
}

// --- claims -----------------------------------------------------------------

// oidcClaims is the subset of the standard claim set this server has a use for.
// Anything else a provider sends is ignored: the account here is described by
// the columns in users, and mapping more of the token into it would be inventing
// policy no admin asked for.
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

// flexBool reads a claim that is a boolean in the specification and a string in
// several implementations of it. A value that is neither is false, which for
// email_verified is the answer that costs nothing.
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

// --- account resolution -----------------------------------------------------

// resolveUser turns a verified identity into the account it signs in as, in
// three steps that are deliberately in this order:
//
//  1. A link already exists for this (provider, subject). The subject is the
//     provider's permanent id for the person, so this is the only match that
//     survives them changing their email.
//  2. No link, but the provider asserts a verified email that an existing
//     account holds. The link is created and that account is used. Verification
//     is the whole safeguard here: a provider that lets a person type any email
//     they like would otherwise be a way to take over any account by name.
//  3. No link and no account. A new one is created, if this provider is allowed
//     to create them.
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
		// Existing account, first time through this provider: link them.
	case errors.Is(err, pgx.ErrNoRows):
		// The provider's own switch, not the settings table's signups_enabled:
		// that one governs the email-and-password form, and the two are separate
		// so an operator can close either without closing the other.
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

// createFederatedUser makes the account. The first one to arrive bootstraps the
// system as an admin, exactly as a password sign-up does -- a deployment whose
// only way in is a provider must still be able to get its first admin.
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
	// The username is ours to choose and has to be unique, but what a provider
	// suggests is not: two people at two providers can both be "ameya". Rather
	// than pre-checking -- which races anyone signing up at the same moment --
	// the insert is retried against the constraint that actually decides.
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
		// The email is unique too, and losing that race means someone else just
		// created the very account this login wants.
		if u, err := h.q.GetUserByEmail(ctx, email); err == nil {
			return u, nil
		}
	}
	return db.User{}, errors.New("could not find a free username for this account")
}

// usernameStrip removes everything a generated username may not contain.
// Narrower than what the column allows, because these are built out of whatever
// a provider returned rather than typed by a person, and that should not be the
// first thing to find a hole in something downstream.
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

// --- small helpers ----------------------------------------------------------

// parseScopes accepts the space-separated form the specification uses and the
// comma-separated one people habitually type, and guarantees openid: without it
// the provider runs a plain OAuth flow and returns no identity token at all.
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

// sameOriginPath narrows a caller-supplied destination to a path on this origin.
// It arrives in the address bar, so "//evil.example" and "https://evil.example"
// are both things someone can put there, and following either would make this
// callback an open redirect that hands over a fresh session on the way out.
func sameOriginPath(raw, fallback string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return fallback
	}
	return raw
}
