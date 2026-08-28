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

const proxyTokenTTL = 5 * time.Minute

const proxyLoginPath = "/login"

const proxySecretMinLen = 32

const proxyAuthConfigDefaultControlURL = "http://localhost:1323"

type proxyAuthConfig struct {
	secret string
	controlURL string
	cookieSecure bool
}

func (c proxyAuthConfig) loginURL() string {
	return strings.TrimRight(c.controlURL, "/") + proxyLoginPath
}

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
		secret = strings.TrimRight(string(b), "\r\n")
	}

	switch {
	case secret == "":
		if prod {
			log.Fatal("PROXY_AUTH_SECRET or PROXY_AUTH_SECRET_FILE must be set when APP_ENV=prod")
		}
		log.Printf("no proxy auth secret configured; %s will return 503", proxyLoginPath)
	case len(secret) < proxySecretMinLen:
		log.Fatalf("the proxy auth secret must be at least %d bytes", proxySecretMinLen)
	}
	return proxyAuthConfig{
		secret:       secret,
		controlURL:   controlURL,
		cookieSecure: prod,
	}
}

type proxyTokenPayload struct {
	Sub string `json:"sub"`
	Aud string `json:"aud"`
	Exp int64  `json:"exp"`
}

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
		return c.Redirect(http.StatusFound, signinReturnURL(rd, host, next))
	}

	mayReach, err := h.mayReachHost(c.Request().Context(), u, host)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not check the vm")
	}
	if !mayReach {
		return echo.NewHTTPError(http.StatusForbidden, "you do not have access to that vm")
	}

	token, err := mintProxyToken(h.proxy.secret, u.Email, host, time.Now())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not mint token")
	}

	q := rdURL.Query()
	q.Set("token", token)
	q.Set("next", next)
	rdURL.RawQuery = q.Encode()
	rdURL.Fragment = ""
	return c.Redirect(http.StatusFound, rdURL.String())
}

func parseRedirectTarget(rd, host string) (*url.URL, error) {
	if host == "" || len(host) > 253 || !hostnamePattern.MatchString(host) {
		return nil, errors.New("host is missing or malformed")
	}
	rdURL, err := url.Parse(rd)
	if err != nil || (rdURL.Scheme != "http" && rdURL.Scheme != "https") {
		return nil, errors.New("rd must be an absolute http(s) url")
	}
	if rdURL.User != nil || !strings.EqualFold(rdURL.Hostname(), host) {
		return nil, errors.New("rd does not point at host")
	}
	return rdURL, nil
}

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
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

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

func safeNextPath(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	if strings.ContainsAny(next, "\r\n") {
		return "/"
	}
	return next
}
