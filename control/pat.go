package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

const patPrefix = "dpat_"

const maxLabelLen = 100

func newPersonalAccessToken() (string, error) {
	raw, err := newRefreshToken()
	if err != nil {
		return "", err
	}
	return patPrefix + raw, nil
}

func authenticatePAT(c *echo.Context, q *db.Queries, raw string) error {
	t, err := q.AuthenticatePersonalAccessToken(c.Request().Context(), hashRefresh(raw))
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired token")
	}
	c.Set("uid", uuid.UUID(t.UserID.Bytes).String())
	c.Set("auth", "pat")
	return nil
}

func denyPAT(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if m, _ := c.Get("auth").(string); m == "pat" {
			return echo.NewHTTPError(http.StatusForbidden, "personal access tokens cannot manage tokens; sign in to do this")
		}
		return next(c)
	}
}

type patDTO struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Prefix string `json:"token_prefix"`
	Status string `json:"status"`
	ExpiresAt  string `json:"expires_at"`
	LastUsedAt string `json:"last_used_at"`
	CreatedAt  string `json:"created_at"`
}

func toPATDTO(t db.PersonalAccessToken) patDTO {
	d := patDTO{
		ID:        uuid.UUID(t.ID.Bytes).String(),
		Label:     t.Label,
		Prefix:    t.TokenPrefix,
		CreatedAt: t.CreatedAt.Time.Format(time.RFC3339),
	}
	if t.ExpiresAt.Valid {
		d.ExpiresAt = t.ExpiresAt.Time.Format(time.RFC3339)
	}
	if t.LastUsedAt.Valid {
		d.LastUsedAt = t.LastUsedAt.Time.Format(time.RFC3339)
	}
	switch {
	case t.Revoked:
		d.Status = "revoked"
	case t.ExpiresAt.Valid && t.ExpiresAt.Time.Before(time.Now()):
		d.Status = "expired"
	default:
		d.Status = "active"
	}
	return d
}

// @Summary     List your personal access tokens
// @Description Only the prefix of each token is returned -- enough to recognise one you still hold, useless to anyone who only has this. Requires a signed-in session: a token cannot enumerate its siblings.
// @Tags        tokens
// @Produce     json
// @Success     200 {object} tokenList
// @Failure     401 {object} apiError
// @Failure     403 {object} apiError "authenticated with a personal access token"
// @Security    BearerAuth
// @Router      /me/tokens [get]
func (h *ProfileHandler) ListTokens(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	rows, err := h.q.ListPersonalAccessTokensByUser(c.Request().Context(), owner)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list your tokens")
	}
	items := make([]patDTO, 0, len(rows))
	for _, t := range rows {
		items = append(items, toPATDTO(t))
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

type createPATReq struct {
	Label string `json:"label"`
	ExpiresInDays *int32 `json:"expires_in_days"`
}

// @Summary     Create a personal access token
// @Description The response is the only place the raw token appears; it is stored hashed and cannot be shown again. Omit expires_in_days for a token that never expires. Requires a signed-in session, so a leaked token cannot mint its successor and outlive the revocation meant to end it.
// @Tags        tokens
// @Accept      json
// @Produce     json
// @Param       body body createPATReq true "a label is required"
// @Success     201 {object} createdTokenResp "the raw token, shown once"
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     403 {object} apiError "authenticated with a personal access token"
// @Security    BearerAuth
// @Router      /me/tokens [post]
func (h *ProfileHandler) CreateToken(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	var req createPATReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "a label is required")
	}
	if len(req.Label) > maxLabelLen {
		return echo.NewHTTPError(http.StatusBadRequest, "a label cannot be longer than 100 characters")
	}
	if req.ExpiresInDays != nil && *req.ExpiresInDays < 1 {
		return echo.NewHTTPError(http.StatusBadRequest, "expires_in_days must be at least 1")
	}

	raw, err := newPersonalAccessToken()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not generate a token")
	}
	params := db.CreatePersonalAccessTokenParams{
		UserID:      owner,
		TokenHash:   hashRefresh(raw),
		TokenPrefix: raw[:len(patPrefix)+8],
		Label:       req.Label,
	}
	if req.ExpiresInDays != nil {
		exp := time.Now().AddDate(0, 0, int(*req.ExpiresInDays))
		params.ExpiresAt = pgtype.Timestamptz{Time: exp, Valid: true}
	}

	t, err := h.q.CreatePersonalAccessToken(c.Request().Context(), params)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not create the token")
	}
	return c.JSON(http.StatusCreated, map[string]any{
		"token":                 raw,
		"personal_access_token": toPATDTO(t),
	})
}

// @Summary     Revoke a personal access token
// @Description Anything using it stops working immediately. The row stays in the list, marked revoked, so the credential's history is still readable.
// @Tags        tokens
// @Produce     json
// @Param       id path string true "token id" format(uuid)
// @Success     204 "revoked"
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     403 {object} apiError "authenticated with a personal access token"
// @Failure     404 {object} apiError "no such token, or it belongs to somebody else"
// @Security    BearerAuth
// @Router      /me/tokens/{id}/revoke [post]
func (h *ProfileHandler) RevokeToken(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid token id")
	}
	n, err := h.q.RevokePersonalAccessToken(c.Request().Context(), db.RevokePersonalAccessTokenParams{
		ID: pgID, UserID: owner,
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not revoke the token")
	}
	if n == 0 {
		return echo.NewHTTPError(http.StatusNotFound, "token not found")
	}
	return c.NoContent(http.StatusNoContent)
}

// @Summary     Delete a personal access token
// @Description Anything using it stops working and no record of it is kept. Revoke instead when you want the row to stay.
// @Tags        tokens
// @Produce     json
// @Param       id path string true "token id" format(uuid)
// @Success     204 "deleted"
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     403 {object} apiError "authenticated with a personal access token"
// @Failure     404 {object} apiError "no such token, or it belongs to somebody else"
// @Security    BearerAuth
// @Router      /me/tokens/{id} [delete]
func (h *ProfileHandler) DeleteToken(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid token id")
	}
	n, err := h.q.DeletePersonalAccessToken(c.Request().Context(), db.DeletePersonalAccessTokenParams{
		ID: pgID, UserID: owner,
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete the token")
	}
	if n == 0 {
		return echo.NewHTTPError(http.StatusNotFound, "token not found")
	}
	return c.NoContent(http.StatusNoContent)
}
