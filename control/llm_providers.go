package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type llmPlan struct {
	ID            string            `json:"id"`
	Label         string            `json:"label"`
	OpenAIBase    string            `json:"-"`
	AnthropicBase string            `json:"-"`
	ResponsesBase string            `json:"-"`
	KeyCheckURL   string            `json:"-"`
	AuthHeaders   map[string]string `json:"-"`
}

// base is where this plan serves the api format names, or "" if it does not.
func (p llmPlan) base(format string) string {
	switch format {
	case "openai":
		return p.OpenAIBase
	case "anthropic":
		return p.AnthropicBase
	case "responses":
		return p.ResponsesBase
	}
	return ""
}

// authHeader is empty for the usual Authorization bearer token. A plan can
// name x-api-key for an api format that follows Anthropic's wire protocol.
func (p llmPlan) authHeader(format string) string {
	return p.AuthHeaders[format]
}

const (
	llmAuthKey    = "key"
	llmAuthDevice = "device"
)

type llmProvider struct {
	ID          string    `json:"id"`
	Label       string    `json:"label"`
	Description string    `json:"description,omitempty"`
	KeyURL      string    `json:"key_url,omitempty"`
	Auth        string    `json:"auth"`
	Plans       []llmPlan `json:"plans"`
}

// llmProviders is every upstream a key can be stored for. The ids are also the
// model prefix a vm uses, so they must stay stable.
var llmProviders = []llmProvider{{
	ID:    "zai",
	Label: "Z.ai",
	Auth:  llmAuthKey,
	Plans: []llmPlan{
		{ID: "api", Label: "API", OpenAIBase: "https://api.z.ai/api/paas/v4", AnthropicBase: "https://api.z.ai/api/anthropic"},
		{ID: "coding", Label: "Coding plan", OpenAIBase: "https://api.z.ai/api/coding/paas/v4", AnthropicBase: "https://api.z.ai/api/anthropic"},
	},
}, {
	ID:          "opencode-go",
	Label:       "OpenCode Go",
	Description: "Works with both Go and Go Plus subscriptions; OpenCode applies the limits for your account.",
	KeyURL:      "https://opencode.ai/auth",
	Auth:        llmAuthKey,
	Plans: []llmPlan{
		{
			ID: "go", Label: "Go / Go Plus",
			OpenAIBase: "https://opencode.ai/zen/go/v1", AnthropicBase: "https://opencode.ai/zen/go",
			ResponsesBase: "https://opencode.ai/zen/go/v1", KeyCheckURL: "https://opencode.ai/zen/go/v1/usage",
			AuthHeaders: map[string]string{"anthropic": "x-api-key"},
		},
	},
}, {
	ID:    "chatgpt",
	Label: "ChatGPT",
	Auth:  llmAuthDevice,
	Plans: []llmPlan{
		{ID: "subscription", Label: "Subscription", ResponsesBase: "https://chatgpt.com/backend-api/codex"},
	},
}}

func llmPlanFor(provider, plan string) (llmPlan, bool) {
	for _, p := range llmProviders {
		if p.ID != provider {
			continue
		}
		for _, pl := range p.Plans {
			if pl.ID == plan {
				return pl, true
			}
		}
	}
	return llmPlan{}, false
}

func llmProviderFor(provider string) (llmProvider, bool) {
	for _, p := range llmProviders {
		if p.ID == provider {
			return p, true
		}
	}
	return llmProvider{}, false
}

func llmUsesDevice(provider string) bool {
	p, _ := llmProviderFor(provider)
	return p.Auth == llmAuthDevice
}

type llmModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

var errLLMKeyRejected = errors.New("the provider rejected this api key")

var llmHTTPClient = &http.Client{Timeout: 15 * time.Second}

func fetchLLMModels(ctx context.Context, plan llmPlan, key string) ([]llmModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, plan.OpenAIBase+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := llmHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, errLLMKeyRejected
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("listing models returned %d", resp.StatusCode)
	}
	var list struct {
		Data []llmModel `json:"data"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("could not decode the model list: %w", err)
	}
	return list.Data, nil
}

// checkLLMKey verifies a credential before it is stored. Most providers
// authenticate their model catalog; OpenCode's catalog is public, so its plan
// points at the authenticated, non-generating usage endpoint instead.
func checkLLMKey(ctx context.Context, plan llmPlan, key string) error {
	if plan.KeyCheckURL == "" {
		_, err := fetchLLMModels(ctx, plan, key)
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, plan.KeyCheckURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := llmHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errLLMKeyRejected
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("checking the key returned %d", resp.StatusCode)
	default:
		return nil
	}
}

const llmModelsTTL = 10 * time.Minute

// llmModelsCache keeps each key's model list for llmModelsTTL, shared across
// every host. A key that is replaced gets a new updated_at and is refetched.
type llmModelsCache struct {
	mu sync.Mutex
	m  map[pgtype.UUID]*llmModelsEntry
}

type llmModelsEntry struct {
	ready   chan struct{}
	version time.Time
	at      time.Time
	models  []llmModel
	err     error
}

func newLLMModelsCache() *llmModelsCache {
	return &llmModelsCache{m: map[pgtype.UUID]*llmModelsEntry{}}
}

func (c *llmModelsCache) get(ctx context.Context, keyID pgtype.UUID, version time.Time, fetch func() ([]llmModel, error)) ([]llmModel, error) {
	c.mu.Lock()
	if e, ok := c.m[keyID]; ok && e.version.Equal(version) {
		select {
		case <-e.ready:
			if e.err == nil && time.Since(e.at) < llmModelsTTL {
				c.mu.Unlock()
				return e.models, nil
			}
		default:
			c.mu.Unlock()
			select {
			case <-e.ready:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return e.models, e.err
		}
	}
	e := &llmModelsEntry{ready: make(chan struct{}), version: version}
	c.m[keyID] = e
	c.mu.Unlock()

	e.models, e.err = fetch()
	e.at = time.Now()
	close(e.ready)

	if e.err != nil {
		c.mu.Lock()
		if c.m[keyID] == e {
			delete(c.m, keyID)
		}
		c.mu.Unlock()
	}
	return e.models, e.err
}
