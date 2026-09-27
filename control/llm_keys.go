package main

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

// LLMKeyHandler stores api keys for llm providers: one per provider for each
// user, and one global key per provider that an admin sets for everyone.
type LLMKeyHandler struct {
	q      *db.Queries
	sealer *llmSealer
}

type llmKeyDTO struct {
	Provider  string    `json:"provider"`
	Label     string    `json:"label"`
	Plans     []llmPlan `json:"plans"`
	Plan      string    `json:"plan"`
	KeySet    bool      `json:"key_set"`
	UpdatedAt string    `json:"updated_at,omitempty"`
}

type putLLMKeyReq struct {
	Plan   string `json:"plan"`
	APIKey string `json:"api_key"`
}

func toLLMKeyDTOs(keys []db.LlmKey) []llmKeyDTO {
	byProvider := map[string]db.LlmKey{}
	for _, k := range keys {
		byProvider[k.Provider] = k
	}
	out := make([]llmKeyDTO, 0, len(llmProviders))
	for _, p := range llmProviders {
		d := llmKeyDTO{Provider: p.ID, Label: p.Label, Plans: p.Plans, Plan: p.Plans[0].ID}
		if k, ok := byProvider[p.ID]; ok {
			d.Plan, d.KeySet = k.Plan, true
			d.UpdatedAt = k.UpdatedAt.Time.Format(time.RFC3339)
		}
		out = append(out, d)
	}
	return out
}

// @Summary     Your llm provider keys
// @Tags        llm
// @Produce     json
// @Router      /me/llm-keys [get]
func (h *LLMKeyHandler) ListMine(c *echo.Context) error {
	uid, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	keys, err := h.q.ListUserLLMKeys(c.Request().Context(), uid)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read your llm keys")
	}
	return c.JSON(http.StatusOK, map[string]any{"items": toLLMKeyDTOs(keys)})
}

// @Summary     Global llm provider keys, usable by everyone
// @Tags        admin
// @Produce     json
// @Router      /admin/llm-keys [get]
func (h *LLMKeyHandler) ListGlobal(c *echo.Context) error {
	keys, err := h.q.ListGlobalLLMKeys(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the global llm keys")
	}
	return c.JSON(http.StatusOK, map[string]any{"items": toLLMKeyDTOs(keys)})
}

// @Summary     Set your key for an llm provider
// @Tags        llm
// @Accept      json
// @Router      /me/llm-keys/{provider} [put]
func (h *LLMKeyHandler) PutMine(c *echo.Context) error {
	uid, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	return h.put(c, uid, uid)
}

// @Summary     Set the global key for an llm provider
// @Tags        admin
// @Accept      json
// @Router      /admin/llm-keys/{provider} [put]
func (h *LLMKeyHandler) PutGlobal(c *echo.Context) error {
	uid, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	return h.put(c, pgtype.UUID{}, uid)
}

// put stores a key for owner, or the global key when owner is not valid. The
// key is checked against the provider first, so a typo fails here and not on
// the first request a vm makes.
func (h *LLMKeyHandler) put(c *echo.Context, owner, by pgtype.UUID) error {
	if h.sealer == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "llm keys are not enabled on this server")
	}
	provider := c.Param("provider")
	if !llmProviderKnown(provider) {
		return echo.NewHTTPError(http.StatusNotFound, "unknown llm provider")
	}
	var req putLLMKeyReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	plan, ok := llmPlanFor(provider, req.Plan)
	if !ok {
		return echo.NewHTTPError(http.StatusBadRequest, "unknown plan for this provider")
	}

	ctx := c.Request().Context()
	aad := llmKeyAAD(provider, owner)
	key := strings.TrimSpace(req.APIKey)

	// An empty key keeps the stored one, which is still re-checked because the
	// plan decides which endpoint it has to work against.
	if key == "" {
		var existing db.LlmKey
		var err error
		if owner.Valid {
			existing, err = h.q.GetUserLLMKey(ctx, db.GetUserLLMKeyParams{OwnerID: owner, Provider: provider})
		} else {
			existing, err = h.q.GetGlobalLLMKey(ctx, provider)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusBadRequest, "an api key is required the first time")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "could not read the stored key")
		}
		if key, err = h.sealer.open(existing.ApiKeyEnc, aad); err != nil {
			log.Printf("could not open the stored %s llm key: %v", provider, err)
			return echo.NewHTTPError(http.StatusInternalServerError, "the stored key could not be decrypted; set it again")
		}
	}

	if _, err := fetchLLMModels(ctx, plan, key); err != nil {
		if errors.Is(err, errLLMKeyRejected) {
			return echo.NewHTTPError(http.StatusBadRequest, "the provider rejected this key for the "+plan.Label+" plan")
		}
		log.Printf("could not check a %s llm key: %v", provider, err)
		return echo.NewHTTPError(http.StatusBadGateway, "could not reach the provider to check this key")
	}

	sealed, err := h.sealer.seal(key, aad)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not encrypt the key")
	}
	if owner.Valid {
		_, err = h.q.UpsertUserLLMKey(ctx, db.UpsertUserLLMKeyParams{
			OwnerID: owner, Provider: provider, Plan: plan.ID, ApiKeyEnc: sealed,
		})
	} else {
		_, err = h.q.UpsertGlobalLLMKey(ctx, db.UpsertGlobalLLMKeyParams{
			Provider: provider, Plan: plan.ID, ApiKeyEnc: sealed, UpdatedBy: by,
		})
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the key")
	}
	return c.NoContent(http.StatusNoContent)
}

// @Summary     Remove your key for an llm provider
// @Tags        llm
// @Router      /me/llm-keys/{provider} [delete]
func (h *LLMKeyHandler) DeleteMine(c *echo.Context) error {
	uid, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	n, err := h.q.DeleteUserLLMKey(c.Request().Context(), db.DeleteUserLLMKeyParams{OwnerID: uid, Provider: c.Param("provider")})
	return deletedOr(c, n, err)
}

// @Summary     Remove the global key for an llm provider
// @Tags        admin
// @Router      /admin/llm-keys/{provider} [delete]
func (h *LLMKeyHandler) DeleteGlobal(c *echo.Context) error {
	n, err := h.q.DeleteGlobalLLMKey(c.Request().Context(), c.Param("provider"))
	return deletedOr(c, n, err)
}

func deletedOr(c *echo.Context, n int64, err error) error {
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not remove the key")
	}
	if n == 0 {
		return echo.NewHTTPError(http.StatusNotFound, "no key is set for this provider")
	}
	return c.NoContent(http.StatusNoContent)
}
