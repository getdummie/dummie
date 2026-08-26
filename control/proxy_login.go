package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

// GET /login is the hand-off between this control plane and the per-host proxy.
// proxy bounces a browser that has no session cookie for a guest here with three
// query params -- rd (its own callback), host (the name being visited) and next
// (the path that was asked for) -- and expects the browser back at rd carrying a
// token it can verify with the key both sides hold.
//
// The token is deliberately not the access-token JWT: that one is this server's
// session credential, and handing a copy of it to every host in the fleet would
// make each of them able to act as the user here. This one says only "this
// identity, for this hostname, for the next five minutes", and the proxy trades
// it for a session cookie of its own.

// proxyTokenTTL is how long a minted token is good for. It is a one-shot
// hand-off that is redeemed within the same redirect, so the window only has to
// cover clock skew between here and the host.
const proxyTokenTTL = 5 * time.Minute

// proxyLoginPath is served by this server, not by the SPA. It is the one
// non-/api/ path that does not go to Nuxt (see proxyToNuxt).
const proxyLoginPath = "/login"

// proxySecretMinLen mirrors what the proxy requires of cookie_secret_file. The
// value is used as raw bytes exactly as it is read -- a hex string is the key,
// not the bytes it spells -- so its length here is its length there.
const proxySecretMinLen = 32

// proxyAuthConfigDefaultControlURL is where a dev box's control server is, and
// matches the default API port. There is no sane default for a real deployment,
// so prod has to say.
const proxyAuthConfigDefaultControlURL = "http://localhost:1323"

// proxyAuthConfig is everything both ends of the hand-off need to agree on: the
// key, where the browser is sent to get a token, and whether the cookie the
// proxy sets afterwards is Secure.
//
// secret is the same bytes as each host's cookie_secret_file -- either point
// PROXY_AUTH_SECRET_FILE at that file, or put the value in PROXY_AUTH_SECRET.
// It is also what the client is told to write into that file, so the two cannot
// drift: there is one value, and it is set here.
type proxyAuthConfig struct {
	secret string
	// controlURL is the origin browsers reach this server on, NOT the address it
	// binds. Behind a proxy or on another network those differ, and the hosts'
	// users are the ones who have to be able to open it.
	controlURL string
	// cookieSecure is what proxy puts on the session cookie it sets after
	// verifying a token. Off in dev because guests are served over plain http
	// there, and a Secure cookie on http is one the browser silently drops.
	cookieSecure bool
}

// loginURL is what goes in the generated dproxy.yaml: the control server's
// origin plus the hand-off path, so there is one place that decides that path.
func (c proxyAuthConfig) loginURL() string {
	return strings.TrimRight(c.controlURL, "/") + proxyLoginPath
}

// cookieSameSite is what the hosts are told to put on the session cookie they
// set. "none" only where guests are served over https, because that is the only
// place a browser keeps such a cookie -- which is the same condition
// cookieSecure already tracks.
func (c proxyAuthConfig) cookieSameSite() string {
	if c.cookieSecure {
		return "none"
	}
	return "lax"
}

func loadProxyAuthConfig(prod bool) proxyAuthConfig {
	controlURL := strings.TrimSpace(os.Getenv("CONTROL_URL"))
	if controlURL == "" {
		if prod {
			log.Fatal("CONTROL_URL must be set when APP_ENV=prod")
		}
		controlURL = proxyAuthConfigDefaultControlURL
	}

	secret := os.Getenv("PROXY_AUTH_SECRET")
	if path := os.Getenv("PROXY_AUTH_SECRET_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			log.Fatalf("could not read PROXY_AUTH_SECRET_FILE %q: %v", path, err)
		}
		// Trailing newline only: the file is shared with the proxy, which reads it
		// as raw bytes, so anything else in it is part of the key on both sides.
		secret = strings.TrimRight(string(b), "\r\n")
	}

	switch {
	case secret == "":
		if prod {
			log.Fatal("PROXY_AUTH_SECRET or PROXY_AUTH_SECRET_FILE must be set when APP_ENV=prod")
		}
		log.Printf("no proxy auth secret configured; %s will return 503", proxyLoginPath)
	case len(secret) < proxySecretMinLen:
		// Fatal in dev too: a short key that still signs tokens would be accepted
		// here and rejected by the proxy, which is a worse failure to debug than
		// not starting.
		log.Fatalf("the proxy auth secret must be at least %d bytes", proxySecretMinLen)
	}
	return proxyAuthConfig{
		secret:       secret,
		controlURL:   controlURL,
		cookieSecure: prod,
	}
}

// proxyTokenPayload is the whole token body. Field order is the emitted JSON
// order, and the proxy verifies the signature over the encoded form, so it never
// has to agree with us about how this marshals.
type proxyTokenPayload struct {
	Sub string `json:"sub"`
	Aud string `json:"aud"`
	Exp int64  `json:"exp"`
}

// mintProxyToken returns "<base64url(payload)>.<base64url(hmac)>", unpadded. The
// MAC is over the base64 text, not the raw JSON: that is what the proxy verifies,
// and it means neither side has to re-serialise anything to check a signature.
func mintProxyToken(secret, sub, aud string, now time.Time) (string, error) {
	payload, err := json.Marshal(proxyTokenPayload{
		Sub: sub,
		Aud: aud,
		Exp: now.Add(proxyTokenTTL).Unix(),
	})
	if err != nil {
		return "", err
	}
	b := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(b))
	return b + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// ProxyLoginHandler serves the hand-off. It reads the session the same way the
// JWT middleware does, but cannot be behind that middleware: an unauthenticated
// visitor here is the normal case, and the answer to it is a redirect to sign in
// rather than a 401.
type ProxyLoginHandler struct {
	q     *db.Queries
	cfg   authConfig
	proxy proxyAuthConfig
}

func (h *ProxyLoginHandler) Login(c *echo.Context) error {
	if h.proxy.secret == "" {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "proxy login is not configured")
	}

	query := c.Request().URL.Query()
	host := strings.ToLower(strings.TrimSpace(query.Get("host")))
	rd := query.Get("rd")
	next := safeNextPath(query.Get("next"))

	rdURL, err := parseRedirectTarget(rd, host)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	allowed, err := h.hostIsOurs(c.Request().Context(), host)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not check the domain")
	}
	if !allowed {
		return echo.NewHTTPError(http.StatusBadRequest, "host is not in an allowed domain")
	}

	u, ok, err := h.sessionUser(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the session")
	}
	if !ok {
		// Sign in first, then come back here with the same three params -- the
		// proxy is not in the loop for this, so losing them would land the user on
		// the dashboard with no way back to the guest they asked for.
		return c.Redirect(http.StatusFound, signinReturnURL(rd, host, next))
	}

	// Being signed in is not being allowed in. aud binds the token to one
	// hostname, which stops it being replayed at another VM, but on its own it
	// would still let any account mint a token for anyone else's guest -- the
	// proxy trusts what this endpoint hands out and has no view of who owns what.
	mayReach, err := h.mayReachHost(c.Request().Context(), u, host)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not check the vm")
	}
	if !mayReach {
		// 403, not a redirect: they are signed in as someone who cannot have this,
		// and bouncing them through sign-in again would just loop.
		return echo.NewHTTPError(http.StatusForbidden, "you do not have access to that vm")
	}

	token, err := mintProxyToken(h.proxy.secret, u.Email, host, time.Now())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not mint token")
	}

	// Set on the parsed URL rather than concatenated, so an rd that already
	// carries a query keeps it and every value is escaped once.
	q := rdURL.Query()
	q.Set("token", token)
	q.Set("next", next)
	rdURL.RawQuery = q.Encode()
	rdURL.Fragment = ""
	return c.Redirect(http.StatusFound, rdURL.String())
}

// parseRedirectTarget is the half of the open-redirect check that does not need
// the database: rd has to be an absolute http(s) url whose host is exactly the
// host the token will be minted for. That pairing is what stops a token ever
// being delivered somewhere it is not valid for; whether the host is one of ours
// at all is hostIsOurs.
func parseRedirectTarget(rd, host string) (*url.URL, error) {
	if host == "" || len(host) > 253 || !hostnamePattern.MatchString(host) {
		return nil, errors.New("host is missing or malformed")
	}
	rdURL, err := url.Parse(rd)
	if err != nil || (rdURL.Scheme != "http" && rdURL.Scheme != "https") {
		return nil, errors.New("rd must be an absolute http(s) url")
	}
	// Userinfo is rejected rather than ignored: "https://vm.example.com@evil.tld"
	// has Hostname() "evil.tld", and while the comparison below already catches
	// that, a url with credentials in it is not a shape anything here produces.
	if rdURL.User != nil || !strings.EqualFold(rdURL.Hostname(), host) {
		return nil, errors.New("rd does not point at host")
	}
	return rdURL, nil
}

// hostIsOurs reports whether host sits under a domain an operator has added.
// Read per request rather than cached: the list is a handful of rows, and a
// domain that was just deleted must stop working now, not after a restart.
func (h *ProxyLoginHandler) hostIsOurs(ctx context.Context, host string) (bool, error) {
	domains, err := h.q.ListDomains(ctx)
	if err != nil {
		return false, err
	}
	for _, d := range domains {
		if host == d.TLD || strings.HasSuffix(host, "."+d.TLD) {
			return true, nil
		}
	}
	return false, nil
}

// mayReachHost reports whether u may be handed a token for host. The hostname is
// "<vm name>.<domain>", so the first label is the VM's name and the rest is the
// domain -- split rather than guessed at, because a VM name never contains a dot
// (vmNamePattern) while a domain routinely does.
func (h *ProxyLoginHandler) mayReachHost(ctx context.Context, u db.User, host string) (bool, error) {
	name, tld, ok := strings.Cut(host, ".")
	if !ok || name == "" || tld == "" {
		return false, nil
	}
	_, err := h.q.GetVMForOwnerByHostname(ctx, db.GetVMForOwnerByHostnameParams{
		Name:      name,
		DomainTLD: tld,
		IsAdmin:   u.UserType == "admin",
		OwnerID:   u.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// No row is "not yours", "not a VM" and "destroyed" alike, and they are
		// answered the same way on purpose: distinguishing them here would turn
		// this endpoint into a way to ask whether a given VM name exists.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// sessionUser resolves the browser's existing session. The access-token cookie
// is tried first, then the refresh cookie: the access token lives five minutes
// and the browser arrives here from another origin's redirect, so it is quite
// normal for the session to be alive with only the refresh cookie still valid.
// A missing/expired/invalid session is (nil, false, nil) -- not an error.
func (h *ProxyLoginHandler) sessionUser(c *echo.Context) (db.User, bool, error) {
	ctx := c.Request().Context()

	if ck, err := c.Request().Cookie("access_token"); err == nil && ck.Value != "" {
		claims := jwt.MapClaims{}
		tok, err := jwt.ParseWithClaims(ck.Value, claims, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return []byte(h.cfg.jwtSecret), nil
		})
		if err == nil && tok.Valid {
			if sub, ok := claims["sub"].(string); ok {
				if id, err := parseUUID(sub); err == nil {
					u, err := h.q.GetUserByID(ctx, id)
					if err == nil {
						return u, true, nil
					}
				}
			}
		}
	}

	ck, err := c.Request().Cookie("refresh_token")
	if err != nil || ck.Value == "" {
		return db.User{}, false, nil
	}
	rt, err := h.q.GetRefreshTokenByHash(ctx, hashRefresh(ck.Value))
	if err != nil || rt.Revoked || rt.ExpiresAt.Time.Before(time.Now()) {
		return db.User{}, false, nil
	}
	u, err := h.q.GetUserByID(ctx, rt.UserID)
	if err != nil {
		return db.User{}, false, nil
	}
	return u, true, nil
}

// signinReturnURL sends the browser to the SPA's sign-in page with a path that
// brings it back here. The params are re-encoded from the validated values
// rather than passed through, so nothing the caller sent is reflected verbatim.
func signinReturnURL(rd, host, next string) string {
	back := url.URL{
		Path: proxyLoginPath,
		RawQuery: url.Values{
			"rd":   {rd},
			"host": {host},
			"next": {next},
		}.Encode(),
	}
	return "/signin?" + url.Values{"redirect": {back.String()}}.Encode()
}

// safeNextPath keeps next to a path on the guest. "//evil.example" and
// "https://evil.example" are both things a browser resolves as another origin,
// and next is handed to the proxy to redirect to after it sets its cookie.
func safeNextPath(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	if strings.ContainsAny(next, "\r\n") {
		return "/"
	}
	return next
}
