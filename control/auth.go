package main

import (
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

type authConfig struct {
	jwtSecret  string
	accessTTL  time.Duration
	refreshTTL time.Duration
	prod       bool
}

func loadAuthConfig() authConfig {
	prod := strings.EqualFold(os.Getenv("APP_ENV"), "prod")
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		if prod {
			log.Fatal("JWT_SECRET must be set when APP_ENV=prod")
		}
		secret = "dev-insecure-secret-change-me"
	}
	return authConfig{
		jwtSecret:  secret,
		accessTTL:  time.Duration(envInt("ACCESS_TOKEN_EXPIRY_MINS", 5)) * time.Minute,
		refreshTTL: time.Duration(envInt("REFRESH_TOKEN_EXPIRY_DAYS", 7)) * 24 * time.Hour,
		prod:       prod,
	}
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

type AuthHandler struct {
	q   *db.Queries
	cfg authConfig
}

type userDTO struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	UserType  string `json:"user_type"`
}

func toUserDTO(u db.User) userDTO {
	return userDTO{
		ID:        uuid.UUID(u.ID.Bytes).String(),
		Username:  u.Username,
		Email:     u.Email,
		FirstName: u.FirstName,
		LastName:  u.LastName,
		UserType:  u.UserType,
	}
}

func (h *AuthHandler) setAccessCookie(c *echo.Context, access string) {
	http.SetCookie(c.Response(), &http.Cookie{
		Name: "access_token", Value: access, Path: "/",
		HttpOnly: true, Secure: h.cfg.prod, SameSite: http.SameSiteLaxMode,
		MaxAge: int(h.cfg.accessTTL.Seconds()),
	})
}

func (h *AuthHandler) setRefreshCookie(c *echo.Context, refresh string) {
	http.SetCookie(c.Response(), &http.Cookie{
		Name: "refresh_token", Value: refresh, Path: "/",
		HttpOnly: true, Secure: h.cfg.prod, SameSite: http.SameSiteLaxMode,
		MaxAge: int(h.cfg.refreshTTL.Seconds()),
	})
}

func (h *AuthHandler) clearAuthCookies(c *echo.Context) {
	for _, name := range []string{"access_token", "refresh_token"} {
		http.SetCookie(c.Response(), &http.Cookie{
			Name: name, Value: "", Path: "/",
			HttpOnly: true, Secure: h.cfg.prod, SameSite: http.SameSiteLaxMode,
			MaxAge: -1,
		})
	}
}

func clientIP(c *echo.Context) string {
	if xff := c.Request().Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(c.Request().RemoteAddr)
	if err != nil {
		return c.Request().RemoteAddr
	}
	return host
}

func (h *AuthHandler) startSession(c *echo.Context, u db.User) (string, error) {
	access, err := newAccessToken(u, h.cfg.jwtSecret, h.cfg.accessTTL)
	if err != nil {
		return "", echo.NewHTTPError(http.StatusInternalServerError, "could not mint token")
	}
	raw, err := newRefreshToken()
	if err != nil {
		return "", echo.NewHTTPError(http.StatusInternalServerError, "could not mint token")
	}
	_, err = h.q.CreateRefreshToken(c.Request().Context(), db.CreateRefreshTokenParams{
		UserID:    u.ID,
		TokenHash: hashRefresh(raw),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(h.cfg.refreshTTL), Valid: true},
		UserAgent: c.Request().UserAgent(),
		IP:        clientIP(c),
	})
	if err != nil {
		return "", echo.NewHTTPError(http.StatusInternalServerError, "could not create session")
	}
	h.setAccessCookie(c, access)
	h.setRefreshCookie(c, raw)
	return access, nil
}

func (h *AuthHandler) issueSession(c *echo.Context, u db.User) error {
	access, err := h.startSession(c, u)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{
		"access_token": access,
		"user":         toUserDTO(u),
	})
}

type signupStatusResp struct {
	Enabled bool `json:"enabled"`
}

// @Summary     Whether sign-ups are open
// @Tags        auth
// @Produce     json
// @Success     200 {object} signupStatusResp
// @Router      /signup_status [get]
func (h *AuthHandler) SignupStatus(c *echo.Context) error {
	enabled := boolSetting(c.Request().Context(), h.q, settingSignupsEnabled)
	return c.JSON(http.StatusOK, signupStatusResp{Enabled: enabled})
}

type signupReq struct {
	Username  string `json:"username"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// @Summary     Create an account
// @Description The first account to sign up becomes the admin; every one after is a regular user. The client does not get to choose. Sets the httpOnly access and refresh cookies as well as returning the access token.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body body signupReq true "username and email are required"
// @Success     200 {object} sessionResp
// @Failure     400 {object} apiError
// @Failure     403 {object} apiError "sign-ups are disabled"
// @Failure     409 {object} apiError "username or email already taken"
// @Router      /signup [post]
func (h *AuthHandler) Signup(c *echo.Context) error {
	var req signupReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Username == "" || req.Email == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "username and email are required")
	}
	if !boolSetting(c.Request().Context(), h.q, settingSignupsEnabled) {
		return echo.NewHTTPError(http.StatusForbidden, "sign-ups are disabled")
	}
	if err := validatePassword(req.Password, h.cfg.prod); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not hash password")
	}
	ctx := c.Request().Context()
	userType := "user"
	if n, err := h.q.CountUsers(ctx); err == nil && n == 0 {
		userType = "admin"
	}
	u, err := h.q.CreateUser(ctx, db.CreateUserParams{
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: hash,
		FirstName:    strings.TrimSpace(req.FirstName),
		LastName:     strings.TrimSpace(req.LastName),
		UserType:     userType,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return echo.NewHTTPError(http.StatusConflict, "username or email already taken")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not create user")
	}
	return h.issueSession(c, u)
}

type signinReq struct {
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}

// @Summary     Sign in
// @Description The identifier is a username (case-sensitive) or an email. Sets the httpOnly access and refresh cookies as well as returning the access token.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body body signinReq true "identifier and password"
// @Success     200 {object} sessionResp
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError "invalid credentials"
// @Router      /signin [post]
func (h *AuthHandler) Signin(c *echo.Context) error {
	var req signinReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	req.Identifier = strings.TrimSpace(req.Identifier)
	if req.Identifier == "" || req.Password == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "identifier and password are required")
	}
	ctx := c.Request().Context()
	u, err := h.q.GetUserByUsernameOrEmail(ctx, req.Identifier)
	if errors.Is(err, pgx.ErrNoRows) {
		u, err = h.q.GetUserByUsernameOrEmail(ctx, strings.ToLower(req.Identifier))
	}
	if err != nil || !checkPassword(u.PasswordHash, req.Password) {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid credentials")
	}
	return h.issueSession(c, u)
}

// @Summary     Refresh the access token
// @Description Reads the httpOnly refresh cookie and mints a new access token. The refresh token is not rotated, so a page reload does not churn sessions. Not needed when calling the API with a personal access token, which does not expire on this schedule.
// @Tags        auth
// @Produce     json
// @Success     200 {object} sessionResp
// @Failure     401 {object} apiError "missing, expired or revoked refresh token"
// @Router      /token_refresh [post]
func (h *AuthHandler) TokenRefresh(c *echo.Context) error {
	cookie, err := c.Request().Cookie("refresh_token")
	if err != nil || cookie.Value == "" {
		return echo.NewHTTPError(http.StatusUnauthorized, "missing refresh token")
	}
	ctx := c.Request().Context()
	rt, err := h.q.GetRefreshTokenByHash(ctx, hashRefresh(cookie.Value))
	if err != nil || rt.Revoked || rt.ExpiresAt.Time.Before(time.Now()) {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid refresh token")
	}
	u, err := h.q.GetUserByID(ctx, rt.UserID)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "user not found")
	}
	access, err := newAccessToken(u, h.cfg.jwtSecret, h.cfg.accessTTL)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not mint token")
	}
	h.setAccessCookie(c, access)
	return c.JSON(http.StatusOK, map[string]any{
		"access_token": access,
		"user":         toUserDTO(u),
	})
}

// @Summary     Sign out
// @Description Ends this browser session. Personal access tokens are untouched: signing out of a laptop must not break a pipeline.
// @Tags        auth
// @Success     204 "signed out"
// @Router      /signout [post]
func (h *AuthHandler) Signout(c *echo.Context) error {
	if cookie, err := c.Request().Cookie("refresh_token"); err == nil && cookie.Value != "" {
		_ = h.q.RevokeRefreshTokenByHash(c.Request().Context(), hashRefresh(cookie.Value))
	}
	h.clearAuthCookies(c)
	return c.NoContent(http.StatusNoContent)
}
