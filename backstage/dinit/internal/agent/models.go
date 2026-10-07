package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// pi reaches the fleet's llm proxy through two providers in its models.json:
// chatgpt is only served as the responses api, everything else as chat
// completions. Only these two keys are ever written; the rest of the file is
// the user's.
const (
	providerChat      = "dummie"
	providerResponses = "dummie-chatgpt"
	modelsTimeout     = 5 * time.Second
	// intproxy brokers the key by the vm's address; pi still wants one to send.
	placeholderKey = "dummie"
)

var visionModel = regexp.MustCompile(`(?i)(\dv$|vision)`)

type llmModel struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by"`
}

type piModel struct {
	ID    string   `json:"id"`
	Input []string `json:"input,omitempty"`
}

type piProvider struct {
	BaseURL string    `json:"baseUrl"`
	API     string    `json:"api"`
	APIKey  string    `json:"apiKey"`
	Models  []piModel `json:"models"`
}

func refreshModels(tld string) error {
	base, models, err := fetchModels(tld)
	if err != nil {
		return err
	}
	var chat, responses []piModel
	for _, m := range models {
		switch {
		case m.OwnedBy == "chatgpt":
			responses = append(responses, piModel{ID: m.ID, Input: []string{"text", "image"}})
		case visionModel.MatchString(m.ID):
			chat = append(chat, piModel{ID: m.ID, Input: []string{"text", "image"}})
		default:
			chat = append(chat, piModel{ID: m.ID})
		}
	}
	return writeProviders(filepath.Join(piDir(), "models.json"), map[string]*piProvider{
		providerChat:      providerOrNil(base, "openai-completions", chat),
		providerResponses: providerOrNil(base, "openai-responses", responses),
	})
}

func providerOrNil(base, api string, models []piModel) *piProvider {
	if len(models) == 0 {
		return nil
	}
	return &piProvider{BaseURL: base, API: api, APIKey: placeholderKey, Models: models}
}

// fetchModels prefers https and falls back to http, for fleets without a
// certificate for the integrations.
func fetchModels(tld string) (string, []llmModel, error) {
	var errs []error
	for _, scheme := range []string{"https", "http"} {
		base := fmt.Sprintf("%s://llm.int.%s/v1", scheme, tld)
		models, err := getModels(base)
		if err == nil {
			return base, models, nil
		}
		errs = append(errs, err)
	}
	return "", nil, errors.Join(errs...)
}

func getModels(base string) ([]llmModel, error) {
	ctx, cancel := context.WithTimeout(context.Background(), modelsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s/models: %s", base, resp.Status)
	}
	var body struct {
		Data []llmModel `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Data, nil
}

type listedModel struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
}

// configuredModels is what models.json declares, for picking a model before
// any pi is running to ask; pi's own list replaces it once a session opens.
func configuredModels() []listedModel {
	out := []listedModel{}
	b, err := os.ReadFile(filepath.Join(piDir(), "models.json"))
	if err != nil {
		return out
	}
	var doc struct {
		Providers map[string]struct {
			Models []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"models"`
		} `json:"providers"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return out
	}
	for name, p := range doc.Providers {
		for _, m := range p.Models {
			out = append(out, listedModel{Provider: name, ID: m.ID, Name: m.Name})
		}
	}
	slices.SortFunc(out, func(a, b listedModel) int {
		return strings.Compare(a.Provider+"/"+a.ID, b.Provider+"/"+b.ID)
	})
	return out
}

// writeProviders sets or removes the named providers and leaves every other
// key in the file as it was. A file that does not parse is left alone.
func writeProviders(path string, providers map[string]*piProvider) error {
	doc := map[string]json.RawMessage{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &doc); err != nil {
			return fmt.Errorf("%s does not parse, so it is left alone: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	existing := map[string]json.RawMessage{}
	if raw, ok := doc["providers"]; ok {
		if err := json.Unmarshal(raw, &existing); err != nil {
			return fmt.Errorf("%s has providers that do not parse: %w", path, err)
		}
	}
	for name, p := range providers {
		if p == nil {
			delete(existing, name)
			continue
		}
		b, err := json.Marshal(p)
		if err != nil {
			return err
		}
		existing[name] = b
	}
	pb, err := json.Marshal(existing)
	if err != nil {
		return err
	}
	doc["providers"] = pb
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".dummie.tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
