package main

import (
	"errors"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"control/internal/db"
	"control/internal/proto"
)

const (
	integrationKindLLM = "llm"
	llmGlobalSuffix    = "@global"
)

var llmResourceRe = regexp.MustCompile(`^([a-z0-9]+(?:-[a-z0-9]+)*)(@global)?/(openai|anthropic|responses)$`)

var llmFormatPaths = map[string]string{
	"openai":    "/v1/chat/completions",
	"anthropic": "/v1/messages",
	"responses": "/v1/responses",
}

// llmToken hands intproxy the key a vm's model prefix names: the vm owner's
// own, or with @global the one an admin set for everyone.
func (h *ClientHandler) llmToken(c *echo.Context, client db.Client, vmIP, resource string) error {
	if h.llmSealer == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "llm keys are not enabled on the control server")
	}
	m := llmResourceRe.FindStringSubmatch(resource)
	if m == nil {
		return echo.NewHTTPError(http.StatusBadRequest, "resource must be <provider>[@global]/<openai|anthropic|responses>")
	}
	provider, global, format := m[1], m[2] != "", m[3]

	keys, err := h.q.ListLLMKeysForVM(c.Request().Context(), db.ListLLMKeysForVMParams{ClientID: client.ID, VMIP: vmIP})
	if err != nil {
		log.Printf("could not read the llm keys for %s on client %s: %v", vmIP, clientLabel(client), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the llm keys for this vm")
	}

	for _, k := range keys {
		if k.Provider != provider || k.IsGlobal != global {
			continue
		}
		plan, ok := llmPlanFor(k.Provider, k.Plan)
		if !ok {
			break
		}
		upstream := plan.base(format)
		if upstream == "" {
			var served []string
			for _, f := range []string{"openai", "anthropic", "responses"} {
				if plan.base(f) != "" {
					served = append(served, llmFormatPaths[f])
				}
			}
			return echo.NewHTTPError(http.StatusForbidden, provider+" does not serve "+llmFormatPaths[format]+"; use "+strings.Join(served, " or "))
		}
		resp := proto.IntegrationTokenResponse{Upstream: upstream, AuthHeader: plan.authHeader(format)}
		if llmUsesDevice(k.Provider) {
			cred, err := h.chatgptAccess(c.Request().Context(), k)
			if errors.Is(err, errLLMKeyRejected) {
				return echo.NewHTTPError(http.StatusForbidden, "the "+provider+" login has expired or was revoked; connect it again under integrations")
			}
			if err != nil {
				log.Printf("could not get a %s login for key %s: %v", provider, domainIDString(k.ID), err)
				return echo.NewHTTPError(http.StatusBadGateway, "could not refresh the "+provider+" login")
			}
			resp.Token, resp.Account, resp.ExpiresAt = cred.AccessToken, cred.AccountID, cred.ExpiresAt
		} else {
			key, err := h.llmSealer.open(k.ApiKeyEnc, llmKeyAAD(k.Provider, llmKeyOwner(k)))
			if err != nil {
				log.Printf("could not open the %s llm key %s: %v", provider, domainIDString(k.ID), err)
				return echo.NewHTTPError(http.StatusInternalServerError, "the stored llm key could not be decrypted")
			}
			resp.Token = key
		}
		// Only global keys are metered: they are the ones someone else pays for.
		if global && k.VMOwner.Valid {
			resp.Meter = domainIDString(k.VMOwner) + "/" + domainIDString(k.VmPk) + "/" + provider
		}
		log.Printf("llm key issued: host=%s vm=%s provider=%s global=%t", clientLabel(client), k.VMName, provider, global)
		return c.JSON(http.StatusOK, resp)
	}

	name := provider
	if global {
		name += llmGlobalSuffix
	}
	log.Printf("llm denied: host=%s vm_ip=%s source=%s reason=no key", clientLabel(client), vmIP, name)
	if global {
		return echo.NewHTTPError(http.StatusForbidden, "no global key is configured for "+provider+"; ask an admin, or use your own key as "+provider+"/<model>")
	}
	return echo.NewHTTPError(http.StatusForbidden, "you have no "+provider+" key configured; add one under integrations, or use the shared one as "+name+llmGlobalSuffix+"/<model>")
}

func llmKeyOwner(k db.ListLLMKeysForVMRow) (owner pgtype.UUID) {
	if !k.IsGlobal {
		owner = k.VMOwner
	}
	return owner
}

// LLMModels lists every model a vm can reach, its owner's keys first. It is
// live from each provider, behind a cache shared by the fleet.
func (h *ClientHandler) LLMModels(c *echo.Context) error {
	client, err := h.authClient(c)
	if err != nil {
		return err
	}
	if h.llmSealer == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "llm keys are not enabled on the control server")
	}
	var req proto.LLMModelsRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "could not decode the request")
	}
	ip := net.ParseIP(strings.TrimSpace(req.VMIP))
	if ip == nil || ip.To4() == nil {
		return echo.NewHTTPError(http.StatusBadRequest, "vm_ip is not an IPv4 address")
	}

	ctx := c.Request().Context()
	keys, err := h.q.ListLLMKeysForVM(ctx, db.ListLLMKeysForVMParams{ClientID: client.ID, VMIP: ip.String()})
	if err != nil {
		log.Printf("could not read the llm keys for %s on client %s: %v", ip, clientLabel(client), err)
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the llm keys for this vm")
	}

	data := []llmModel{}
	for _, k := range keys {
		plan, ok := llmPlanFor(k.Provider, k.Plan)
		if !ok {
			continue
		}
		prefix := k.Provider
		if k.IsGlobal {
			prefix += llmGlobalSuffix
		}
		models, err := h.llmModels.get(ctx, k.ID, k.UpdatedAt.Time, func() ([]llmModel, error) {
			if llmUsesDevice(k.Provider) {
				cred, err := h.chatgptAccess(ctx, k)
				if err != nil {
					return nil, err
				}
				return fetchChatGPTModels(ctx, plan, cred)
			}
			key, err := h.llmSealer.open(k.ApiKeyEnc, llmKeyAAD(k.Provider, llmKeyOwner(k)))
			if err != nil {
				return nil, err
			}
			return fetchLLMModels(ctx, plan, key)
		})
		if err != nil {
			log.Printf("could not list models for %s on vm %s: %v", prefix, k.VMName, err)
			continue
		}
		for _, m := range models {
			m.ID = prefix + "/" + m.ID
			m.Object = "model"
			if m.OwnedBy == "" {
				m.OwnedBy = k.Provider
			}
			data = append(data, m)
		}
	}
	return c.JSON(http.StatusOK, map[string]any{"object": "list", "data": data})
}
