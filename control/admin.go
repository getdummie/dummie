package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

func adminJWT(cfg authConfig) echo.MiddlewareFunc {
	return jwtAuth(cfg, nil, true)
}

func userJWT(cfg authConfig, q *db.Queries) echo.MiddlewareFunc {
	return jwtAuth(cfg, q, false)
}

func jwtAuth(cfg authConfig, pats *db.Queries, requireAdmin bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			raw := ""
			if ck, err := c.Request().Cookie("access_token"); err == nil {
				raw = ck.Value
			}
			if raw == "" {
				if h := c.Request().Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
					raw = strings.TrimPrefix(h, "Bearer ")
				}
			}
			if raw == "" {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing access token")
			}

			if pats != nil && strings.HasPrefix(raw, patPrefix) {
				if err := authenticatePAT(c, pats, raw); err != nil {
					return err
				}
				return next(c)
			}

			claims := jwt.MapClaims{}
			tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
				}
				return []byte(cfg.jwtSecret), nil
			})
			if err != nil || !tok.Valid {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired token")
			}
			if requireAdmin && claims["user_type"] != "admin" {
				return echo.NewHTTPError(http.StatusForbidden, "admin access required")
			}
			if sub, ok := claims["sub"].(string); ok {
				c.Set("uid", sub)
			}
			return next(c)
		}
	}
}

type AdminHandler struct {
	q   *db.Queries
	cfg authConfig
	hub *Hub
	controlURL string
	blobs *blobStore
	pool *pgxpool.Pool
	tasks *taskRunner
	certs *certIssuer
	proxy proxyAuthConfig
}

func pageParams(c *echo.Context) (limit, offset int32) {
	limit, offset = 20, 0
	q := c.Request().URL.Query()
	if n, err := strconv.Atoi(q.Get("limit")); err == nil {
		limit = int32(n)
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	if n, err := strconv.Atoi(q.Get("offset")); err == nil && n >= 0 {
		offset = int32(n)
	}
	return limit, offset
}

// withdrawnParam asks a catalogue listing for its withdrawn side instead of its
// live one, which is where an admin goes to purge what soft delete left behind.
func withdrawnParam(c *echo.Context) bool {
	v := c.Request().URL.Query().Get("withdrawn")
	return v == "1" || strings.EqualFold(v, "true")
}

func pageEnvelope(items any, total int64, limit, offset int32) map[string]any {
	return map[string]any{"items": items, "total": total, "limit": limit, "offset": offset}
}

func parseUUID(s string) (pgtype.UUID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: u, Valid: true}, nil
}

type adminUserDTO struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	UserType  string `json:"user_type"`
	PublicKey string `json:"public_key"`
	VCPULimit      int32  `json:"vcpu_limit"`
	MemoryLimitMiB int32  `json:"memory_limit_mib"`
	DiskLimitMiB   int32  `json:"disk_limit_mib"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

func toAdminUserDTO(u db.User) adminUserDTO {
	return adminUserDTO{
		ID:             uuid.UUID(u.ID.Bytes).String(),
		Username:       u.Username,
		Email:          u.Email,
		FirstName:      u.FirstName,
		LastName:       u.LastName,
		UserType:       u.UserType,
		PublicKey:      u.PublicKey,
		VCPULimit:      u.VCPULimit,
		MemoryLimitMiB: u.MemoryLimitMiB,
		DiskLimitMiB:   u.DiskLimitMiB,
		CreatedAt:      u.CreatedAt.Time.Format(time.RFC3339),
		UpdatedAt:      u.UpdatedAt.Time.Format(time.RFC3339),
	}
}

func (h *AdminHandler) ListUsers(c *echo.Context) error {
	ctx := c.Request().Context()
	limit, offset := pageParams(c)
	total, err := h.q.CountUsers(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not count users")
	}
	rows, err := h.q.ListUsers(ctx, db.ListUsersParams{Limit: limit, Offset: offset})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list users")
	}
	items := make([]adminUserDTO, 0, len(rows))
	for _, u := range rows {
		items = append(items, toAdminUserDTO(u))
	}
	return c.JSON(http.StatusOK, pageEnvelope(items, total, limit, offset))
}

type adminCreateUserReq struct {
	Username  string `json:"username"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	UserType  string `json:"user_type"`
}

func (h *AdminHandler) CreateUser(c *echo.Context) error {
	var req adminCreateUserReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Username == "" || req.Email == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "username and email are required")
	}
	userType := "user"
	if req.UserType == "admin" {
		userType = "admin"
	}
	if err := validatePassword(req.Password, h.cfg.prod); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not hash password")
	}
	u, err := h.q.CreateUser(c.Request().Context(), db.CreateUserParams{
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
	return c.JSON(http.StatusCreated, toAdminUserDTO(u))
}

func (h *AdminHandler) GetUser(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid user id")
	}
	u, err := h.q.GetUserByID(c.Request().Context(), pgID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not load user")
	}
	return c.JSON(http.StatusOK, toAdminUserDTO(u))
}

type updateUserQuotaReq struct {
	VCPULimit      int32 `json:"vcpu_limit"`
	MemoryLimitMiB int32 `json:"memory_limit_mib"`
	DiskLimitMiB   int32 `json:"disk_limit_mib"`
}

func (h *AdminHandler) UpdateUserQuota(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid user id")
	}
	var req updateUserQuotaReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if req.VCPULimit < 1 {
		return echo.NewHTTPError(http.StatusBadRequest, "vcpu_limit must be at least 1")
	}
	if req.MemoryLimitMiB < 128 {
		return echo.NewHTTPError(http.StatusBadRequest, "memory_limit_mib must be at least 128")
	}
	if req.DiskLimitMiB < 1024 {
		return echo.NewHTTPError(http.StatusBadRequest, "disk_limit_mib must be at least 1024")
	}

	u, err := h.q.UpdateUserQuota(c.Request().Context(), db.UpdateUserQuotaParams{
		ID:             pgID,
		VCPULimit:      req.VCPULimit,
		MemoryLimitMiB: req.MemoryLimitMiB,
		DiskLimitMiB:   req.DiskLimitMiB,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save quota")
	}
	return c.JSON(http.StatusOK, toAdminUserDTO(u))
}

type updateUserPublicKeyReq struct {
	PublicKey string `json:"public_key"`
}

func (h *AdminHandler) UpdateUserPublicKey(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid user id")
	}
	var req updateUserPublicKeyReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	key, err := normalizePublicKey(req.PublicKey)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	u, err := h.q.UpdateUserPublicKey(c.Request().Context(), db.UpdateUserPublicKeyParams{
		ID:        pgID,
		PublicKey: key,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "user not found")
		}
		if isDuplicatePublicKey(err) {
			return echo.NewHTTPError(http.StatusConflict, "that public key is already on another account; a key identifies one user")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the public key")
	}
	return c.JSON(http.StatusOK, toAdminUserDTO(u))
}

func (h *AdminHandler) DeleteUser(c *echo.Context) error {
	id := c.Param("id")
	if uid, _ := c.Get("uid").(string); uid != "" && uid == id {
		return echo.NewHTTPError(http.StatusBadRequest, "you cannot delete your own account")
	}
	pgID, err := parseUUID(id)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid user id")
	}
	if err := h.q.DeleteUser(c.Request().Context(), pgID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete user")
	}
	return c.NoContent(http.StatusNoContent)
}

type tokenDTO struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at"`
	CreatedAt string `json:"created_at"`
	UserAgent string `json:"user_agent"`
	IP        string `json:"ip"`
}

func (h *AdminHandler) ListTokens(c *echo.Context) error {
	ctx := c.Request().Context()
	limit, offset := pageParams(c)
	total, err := h.q.CountRefreshTokens(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not count tokens")
	}
	rows, err := h.q.ListRefreshTokens(ctx, db.ListRefreshTokensParams{Limit: limit, Offset: offset})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list tokens")
	}
	now := time.Now()
	items := make([]tokenDTO, 0, len(rows))
	for _, r := range rows {
		status := "active"
		switch {
		case r.Revoked:
			status = "revoked"
		case r.ExpiresAt.Time.Before(now):
			status = "expired"
		}
		items = append(items, tokenDTO{
			ID:        uuid.UUID(r.ID.Bytes).String(),
			Username:  r.Username,
			Email:     r.Email,
			Status:    status,
			ExpiresAt: r.ExpiresAt.Time.Format(time.RFC3339),
			CreatedAt: r.CreatedAt.Time.Format(time.RFC3339),
			UserAgent: r.UserAgent,
			IP:        r.IP,
		})
	}
	return c.JSON(http.StatusOK, pageEnvelope(items, total, limit, offset))
}

func (h *AdminHandler) BlacklistToken(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid token id")
	}
	if err := h.q.RevokeRefreshTokenByID(c.Request().Context(), pgID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not blacklist token")
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *AdminHandler) DeleteToken(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid token id")
	}
	if err := h.q.DeleteRefreshTokenByID(c.Request().Context(), pgID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete token")
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *AdminHandler) CleanupTokens(c *echo.Context) error {
	n, err := h.q.DeleteExpiredRefreshTokens(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not clean up tokens")
	}
	return c.JSON(http.StatusOK, map[string]any{"deleted": n})
}
