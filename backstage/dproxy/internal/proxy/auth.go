package proxy

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"dproxy/internal/httpsniff"
)

// CallbackPath is where the control server sends the user back after a
// successful login. It is exempt from the auth check on every host.
const CallbackPath = "/__auth/callback"

const defaultCookieName = "dpipe_session"
const defaultCookieTTL = 12 * time.Hour

// consoleAudPrefix separates the two things the control server signs with the
// same key. A session token's aud is the bare hostname; a console token's aud is
// "console:" + that hostname. The prefix is the only difference between the two
// payloads, so both verifiers check it explicitly: a bare-hostname aud must
// never open a shell, and a console aud must never pass as a session.
const consoleAudPrefix = "console:"

// Authenticator verifies session cookies and the login tokens the control
// server mints. The proxy owns this because policy belongs in the process that
// can be restarted; dpipe never sees the secret.
type Authenticator struct {
	cfg    AuthConfig
	secret []byte
	now    func() time.Time
}

// claims is the signed payload of both a login token and a session cookie.
type claims struct {
	Sub string `json:"sub"` // who the control server says this is
	Aud string `json:"aud"` // hostname for a session, "console:"+hostname for a console
	Exp int64  `json:"exp"` // unix seconds
}

// NewAuthenticator loads the shared secret. A nil AuthConfig means no host can
// require auth, which Validate already enforces.
func NewAuthenticator(cfg *AuthConfig) (*Authenticator, error) {
	if cfg == nil {
		return nil, nil
	}
	secret, err := os.ReadFile(cfg.CookieSecretFile)
	if err != nil {
		return nil, fmt.Errorf("auth.cookie_secret_file: %w", err)
	}
	secret = bytes.TrimSpace(secret)
	if len(secret) < 32 {
		return nil, errors.New("auth.cookie_secret_file: need at least 32 bytes")
	}
	return &Authenticator{cfg: *cfg, secret: secret, now: time.Now}, nil
}

// CookieName is the session cookie the proxy sets and reads.
func (a *Authenticator) CookieName() string {
	if a.cfg.CookieName != "" {
		return a.cfg.CookieName
	}
	return defaultCookieName
}

func (a *Authenticator) ttl() time.Duration {
	return a.cfg.CookieTTL.Or(defaultCookieTTL)
}

// Mint returns a signed value scoped to host and expiring after the TTL.
func (a *Authenticator) Mint(sub, host string) string {
	payload, _ := json.Marshal(claims{Sub: sub, Aud: host, Exp: a.now().Add(a.ttl()).Unix()})
	b := base64.RawURLEncoding.EncodeToString(payload)
	return b + "." + base64.RawURLEncoding.EncodeToString(a.sign([]byte(b)))
}

// Verify checks the signature, the expiry and that the value was issued as a
// session for host, so a value minted for one VM cannot be replayed against
// another and a console token cannot be spent as a session.
func (a *Authenticator) Verify(value, host string) (string, bool) {
	if strings.HasPrefix(host, consoleAudPrefix) {
		return "", false // never let a caller ask for a console audience here
	}
	return a.verify(value, host)
}

// VerifyConsole checks a console token: same signature and expiry rules, but the
// audience must be the console audience of host, never the bare hostname.
func (a *Authenticator) VerifyConsole(value, host string) (string, bool) {
	if host == "" {
		return "", false
	}
	return a.verify(value, consoleAudPrefix+host)
}

// verify checks the MAC, decodes the payload and requires an exact audience
// match. The audience carries the console/session distinction, so it is compared
// whole rather than by prefix.
func (a *Authenticator) verify(value, aud string) (string, bool) {
	b, sig, found := strings.Cut(value, ".")
	if !found {
		return "", false
	}
	want, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || subtle.ConstantTimeCompare(want, a.sign([]byte(b))) != 1 {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(b)
	if err != nil {
		return "", false
	}
	var c claims
	if err := json.Unmarshal(raw, &c); err != nil {
		return "", false
	}
	if c.Aud == "" || c.Aud != aud || a.now().Unix() >= c.Exp {
		return "", false
	}
	return c.Sub, true
}

func (a *Authenticator) sign(b []byte) []byte {
	m := hmac.New(sha256.New, a.secret)
	m.Write(b)
	return m.Sum(nil)
}

// LoginURL is where an unauthenticated user is sent. The control server is
// expected to authenticate them and redirect back to rd with a ?token= it
// signed with the same secret.
func (a *Authenticator) LoginURL(host, path string) string {
	rd := "http://" + host + CallbackPath
	if a.cfg.CookieSecure {
		rd = "https://" + host + CallbackPath
	}
	q := url.Values{"rd": {rd}, "host": {host}, "next": {path}}
	sep := "?"
	if strings.Contains(a.cfg.ControlURL, "?") {
		sep = "&"
	}
	return a.cfg.ControlURL + sep + q.Encode()
}

// SetCookie renders the Set-Cookie header for a freshly minted session. No
// Domain attribute: the cookie stays scoped to the single host that set it.
//
// SameSite is what decides whether a guest can be embedded at all -- see
// AuthConfig.CookieSameSite. Partitioned rides with None rather than being its
// own setting: a cookie that may cross sites should be keyed to the site it
// crossed from, and the two are only useful together.
func (a *Authenticator) SetCookie(value string) string {
	c := &http.Cookie{
		Name:     a.CookieName(),
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(a.ttl().Seconds()),
	}
	if a.embeddable() {
		c.SameSite = http.SameSiteNoneMode
		// Not conditional on the config: the browser will not keep this cookie
		// without it, and Validate has already refused the combination that would
		// reach here with plaintext guests.
		c.Secure = true
		c.Partitioned = true
	}
	return c.String()
}

// embeddable reports whether sessions are set up to survive being sent from a
// page on another site.
func (a *Authenticator) embeddable() bool {
	return strings.EqualFold(strings.TrimSpace(a.cfg.CookieSameSite), "none")
}

// authVerdict is the auth policy's answer for one request. ok means it may reach
// the backend; otherwise status is the response to send, a 302 to location when
// the user has somewhere to go and setCookie when a session was just minted.
type authVerdict struct {
	ok        bool
	status    int
	location  string
	setCookie string
}

// authorizeRequest is the single auth decision point for both ingresses: the
// plaintext path writes the verdict to the client itself, the https path returns
// it to dpipe in the resolve reply. It fails closed — anything it cannot check
// is unauthenticated.
func authorizeRequest(log *slog.Logger, router *Router, a *Authenticator, host string, req *http.Request) authVerdict {
	entry, ok := router.HostEntry(host)
	if !ok || !entry.NeedsAuth(entry.DefaultPort) {
		return authVerdict{ok: true}
	}
	if a == nil {
		// Validate refuses this combination; do not serve a protected host if it
		// is ever reached anyway.
		log.Warn("http auth: protected host with no authenticator", "host", host)
		return authVerdict{status: http.StatusUnauthorized}
	}
	// Tokens are scoped to the bare hostname, so a dev listener on a non-default
	// port does not change what the control server has to sign.
	host = httpsniff.NormalizeHost(host)

	// The callback is how a user becomes authenticated, so it cannot itself
	// require authentication.
	if req.URL.Path == CallbackPath {
		return completeLogin(log, a, host, req)
	}

	if c, err := req.Cookie(a.CookieName()); err == nil {
		if sub, ok := a.Verify(c.Value, host); ok {
			log.Info("http auth ok", "host", host, "sub", sub)
			return authVerdict{ok: true}
		}
		log.Info("http auth: rejected cookie", "host", host)
	}

	if !wantsHTML(req) {
		log.Info("http auth: unauthenticated non-browser request", "host", host, "path", req.URL.Path)
		return authVerdict{status: http.StatusUnauthorized}
	}
	log.Info("http auth: redirecting to control server", "host", host, "path", req.URL.Path)
	return authVerdict{status: http.StatusFound, location: a.LoginURL(host, req.URL.RequestURI())}
}

// completeLogin handles CallbackPath: verify the token the control server signed
// and swap it for a host-scoped session cookie.
func completeLogin(log *slog.Logger, a *Authenticator, host string, req *http.Request) authVerdict {
	sub, ok := a.Verify(req.URL.Query().Get("token"), host)
	if !ok {
		log.Warn("http auth: invalid callback token", "host", host)
		return authVerdict{status: http.StatusUnauthorized}
	}
	// Never bounce to an attacker-supplied absolute URL, and never let a decoded
	// CRLF forge headers in the redirect that carries the session cookie.
	next := req.URL.Query().Get("next")
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\r\n") {
		next = "/"
	}
	log.Info("http auth: session established", "host", host, "sub", sub)
	return authVerdict{
		status:    http.StatusFound,
		location:  next,
		setCookie: a.SetCookie(a.Mint(sub, host)),
	}
}

// parseRequest recovers the request line and headers from the sniffed prefix.
// The body is irrelevant here and is left in the prefix for replay.
func parseRequest(prefix []byte) (*http.Request, error) {
	return http.ReadRequest(bufio.NewReader(bytes.NewReader(prefix)))
}

// wantsHTML reports whether a redirect is a useful answer for this client. API
// clients and websocket upgrades get a 401 instead.
func wantsHTML(r *http.Request) bool {
	if r.Header.Get("Upgrade") != "" {
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func writeRedirect(w io.Writer, location string, setCookie string) {
	var b strings.Builder
	fmt.Fprintf(&b, "HTTP/1.1 302 Found\r\nLocation: %s\r\n", location)
	if setCookie != "" {
		fmt.Fprintf(&b, "Set-Cookie: %s\r\n", setCookie)
	}
	b.WriteString("Content-Length: 0\r\nConnection: close\r\n\r\n")
	_, _ = io.WriteString(w, b.String())
}

func writeUnauthorized(w io.Writer) {
	const body = "401 Unauthorized\n"
	_, _ = fmt.Fprintf(w, "HTTP/1.1 401 Unauthorized\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %s\r\nConnection: close\r\n\r\n%s",
		strconv.Itoa(len(body)), body)
}
