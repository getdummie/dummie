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

// patPrefix marks a raw personal access token. It is what lets one Bearer
// header carry either credential without ambiguity, and it is why a leaked
// token is greppable in a log or a repository scan.
const patPrefix = "dpat_"

// maxLabelLen bounds the one free-text field. The column is unbounded TEXT and
// the label only ever has to say which machine holds the token.
const maxLabelLen = 100

// newPersonalAccessToken returns the raw token to hand to the caller. Same
// entropy as a refresh token, with the prefix carried in the string so the
// middleware can tell the two apart before it touches the database.
func newPersonalAccessToken() (string, error) {
	raw, err := newRefreshToken()
	if err != nil {
		return "", err
	}
	return patPrefix + raw, nil
}

// authenticatePAT resolves a raw token to a caller. It is only ever reached
// from the non-admin gate: adminJWT passes a nil *db.Queries, so an admin route
// has no code path that consults this table at all. That is the guarantee, not
// a check somewhere further down -- a token cannot reach admin because nothing
// admin-side knows how to read one.
func authenticatePAT(c *echo.Context, q *db.Queries, raw string) error {
	t, err := q.AuthenticatePersonalAccessToken(c.Request().Context(), hashRefresh(raw))
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired token")
	}
	c.Set("uid", uuid.UUID(t.UserID.Bytes).String())
	c.Set("auth", "pat")
	return nil
}

// denyPAT closes the loop a token could otherwise walk round: minting a fresh
// token with an old one, so a leak survives the revocation that was meant to
// end it. Managing tokens takes the password-backed session that created the
// first one.
func denyPAT(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if m, _ := c.Get("auth").(string); m == "pat" {
			return echo.NewHTTPError(http.StatusForbidden, "personal access tokens cannot manage tokens; sign in to do this")
		}
		return next(c)
	}
}

// --- handlers ---------------------------------------------------------------

type patDTO struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Prefix string `json:"token_prefix"`
	Status string `json:"status"` // active | revoked | expired
	// "" for the two that have not happened: a token that never expires, and one
	// that has never been used.
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
	// null/omitted = never expires.
	ExpiresInDays *int32 `json:"expires_in_days"`
}

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
	// The only response that carries the raw token. Nothing stores it, so this
	// body is the user's one chance to copy it.
	return c.JSON(http.StatusCreated, map[string]any{
		"token":                 raw,
		"personal_access_token": toPATDTO(t),
	})
}

func (h *ProfileHandler) RevokeToken(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid token id")
	}
	// Scoped by owner in the statement itself, so another user's id reaches
	// nothing and the miss is indistinguishable from a token that never existed.
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
