package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestPersonalAccessTokenCannotReachAdminRoutes(t *testing.T) {
	e := echo.New()
	admin := e.Group("/admin", adminJWT(authConfig{jwtSecret: "test-secret"}))
	admin.GET("/users", func(c *echo.Context) error { return c.NoContent(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+patPrefix+"whatever-a-real-token-would-look-like")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d: a personal access token must not authenticate an admin route", rec.Code, http.StatusUnauthorized)
	}
}

func TestDenyPATBlocksTokenAuthenticatedCallers(t *testing.T) {
	asPAT := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Set("auth", "pat")
			return next(c)
		}
	}
	e := echo.New()
	g := e.Group("/me/tokens", asPAT, denyPAT)
	g.POST("", func(c *echo.Context) error { return c.NoContent(http.StatusCreated) })

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/me/tokens", nil))

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d: a token must not be able to mint its successor", rec.Code, http.StatusForbidden)
	}
}

func TestNewPersonalAccessTokenIsPrefixedAndUnique(t *testing.T) {
	a, err := newPersonalAccessToken()
	if err != nil {
		t.Fatalf("could not mint: %v", err)
	}
	if !strings.HasPrefix(a, patPrefix) {
		t.Errorf("token %q does not carry the %q prefix the middleware routes on", a, patPrefix)
	}
	if len(a) <= len(patPrefix)+8 {
		t.Errorf("token %q is too short to take a display prefix from", a)
	}
	b, err := newPersonalAccessToken()
	if err != nil {
		t.Fatalf("could not mint: %v", err)
	}
	if a == b {
		t.Error("two mints produced the same token")
	}
}
