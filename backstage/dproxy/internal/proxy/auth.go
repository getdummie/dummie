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

const CallbackPath = "/__auth/callback"

const defaultCookieName = "dpipe_session"
const defaultCookieTTL = 12 * time.Hour

const consoleAudPrefix = "console:"

type Authenticator struct {
	cfg    AuthConfig
	secret []byte
	now    func() time.Time
}

type claims struct {
	Sub string `json:"sub"`
	Aud string `json:"aud"`
	Exp int64  `json:"exp"`
}

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

func (a *Authenticator) CookieName() string {
	if a.cfg.CookieName != "" {
		return a.cfg.CookieName
	}
	return defaultCookieName
}

func (a *Authenticator) ttl() time.Duration {
	return a.cfg.CookieTTL.Or(defaultCookieTTL)
}

func (a *Authenticator) Mint(sub, host string) string {
	payload, _ := json.Marshal(claims{Sub: sub, Aud: host, Exp: a.now().Add(a.ttl()).Unix()})
	b := base64.RawURLEncoding.EncodeToString(payload)
	return b + "." + base64.RawURLEncoding.EncodeToString(a.sign([]byte(b)))
}

func (a *Authenticator) Verify(value, host string) (string, bool) {
	if strings.HasPrefix(host, consoleAudPrefix) {
		return "", false
	}
	return a.verify(value, host)
}

func (a *Authenticator) VerifyConsole(value, host string) (string, bool) {
	if host == "" {
		return "", false
	}
	return a.verify(value, consoleAudPrefix+host)
}

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
		c.Secure = true
		c.Partitioned = true
	}
	return c.String()
}

func (a *Authenticator) embeddable() bool {
	return strings.EqualFold(strings.TrimSpace(a.cfg.CookieSameSite), "none")
}

type authVerdict struct {
	ok        bool
	status    int
	location  string
	setCookie string
}

func authorizeRequest(log *slog.Logger, router *Router, a *Authenticator, host string, req *http.Request) authVerdict {
	entry, ok := router.HostEntry(host)
	if !ok || !entry.NeedsAuth(entry.DefaultPort) {
		return authVerdict{ok: true}
	}
	if a == nil {
		log.Warn("http auth: protected host with no authenticator", "host", host)
		return authVerdict{status: http.StatusUnauthorized}
	}
	host = httpsniff.NormalizeHost(host)

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

func completeLogin(log *slog.Logger, a *Authenticator, host string, req *http.Request) authVerdict {
	sub, ok := a.Verify(req.URL.Query().Get("token"), host)
	if !ok {
		log.Warn("http auth: invalid callback token", "host", host)
		return authVerdict{status: http.StatusUnauthorized}
	}
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

func parseRequest(prefix []byte) (*http.Request, error) {
	return http.ReadRequest(bufio.NewReader(bytes.NewReader(prefix)))
}

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
