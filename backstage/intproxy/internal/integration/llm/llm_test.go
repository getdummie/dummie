package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"intproxy/internal/integration"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name, method, path, body string
		ok                       bool
		kind, resource, relay    string
	}{
		{"models", http.MethodGet, "/v1/models", "", true, kindModels, "", RelayModels},
		{"own key", http.MethodPost, "/v1/chat/completions", `{"model":"zai/glm-4.6"}`, true, kindOpenAI, "zai/openai", ""},
		{"global key", http.MethodPost, "/v1/messages", `{"model":"zai@global/glm-4.6"}`, true, kindAnthropic, "zai@global/anthropic", ""},
		{"no source", http.MethodPost, "/v1/chat/completions", `{"model":"glm-4.6"}`, false, "", "", ""},
		{"bad source", http.MethodPost, "/v1/chat/completions", `{"model":"zai@team/glm-4.6"}`, false, "", "", ""},
		{"empty model", http.MethodPost, "/v1/chat/completions", `{"model":"zai/"}`, false, "", "", ""},
		{"not json", http.MethodPost, "/v1/chat/completions", `nope`, false, "", "", ""},
		{"wrong method", http.MethodGet, "/v1/chat/completions", "", false, "", "", ""},
		{"unknown path", http.MethodPost, "/v1/embeddings", `{"model":"zai/x"}`, false, "", "", ""},
	}
	l := &LLM{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			route, ok := l.Classify(r)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if route.Kind != tc.kind || route.Resource != tc.resource || route.Relay != tc.relay {
				t.Fatalf("route = %+v", route)
			}
		})
	}
}

// Upstream only knows its own model names; the source is ours.
func TestClassifyStripsTheSourceFromTheBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"zai@global/glm-4.6","stream":true,"messages":[]}`))
	if _, ok := (&LLM{}).Classify(r); !ok {
		t.Fatal("not classified")
	}
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "glm-4.6" || body["stream"] != true {
		t.Fatalf("body = %s", raw)
	}
	if r.ContentLength != int64(len(raw)) {
		t.Fatalf("ContentLength = %d, body is %d", r.ContentLength, len(raw))
	}
}

func TestRenderErrorMatchesTheCallersAPI(t *testing.T) {
	l := &LLM{}
	_, body := l.RenderError(integration.Route{Kind: kindAnthropic}, 403, "no key")
	var a struct {
		Type  string
		Error struct{ Message string }
	}
	if err := json.Unmarshal(body, &a); err != nil || a.Type != "error" || a.Error.Message != "no key" {
		t.Fatalf("anthropic body = %s", body)
	}

	_, body = l.RenderError(integration.Route{Kind: kindOpenAI}, 403, "no key")
	var o struct {
		Error struct{ Message string }
	}
	if err := json.Unmarshal(body, &o); err != nil || o.Error.Message != "no key" {
		t.Fatalf("openai body = %s", body)
	}
}
