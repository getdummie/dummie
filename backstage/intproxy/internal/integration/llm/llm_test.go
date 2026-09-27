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
		{"responses", http.MethodPost, "/v1/responses", `{"model":"chatgpt/gpt-5-codex"}`, true, kindResponses, "chatgpt/responses", ""},
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

func codexed(t *testing.T, body string) map[string]any {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(body))
	codexBody(r)
	raw, _ := io.ReadAll(r.Body)
	if r.ContentLength != int64(len(raw)) {
		t.Fatalf("ContentLength = %d, body is %d", r.ContentLength, len(raw))
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("body = %s", raw)
	}
	return out
}

func TestCodexBodyMovesTheSystemPromptIntoInstructions(t *testing.T) {
	out := codexed(t, `{"model":"gpt-6-luna","store":true,"max_output_tokens":256,"input":[`+
		`{"role":"developer","content":"be terse"},`+
		`{"role":"system","content":[{"type":"input_text","text":"no emoji"}]},`+
		`{"role":"user","content":"hi"}]}`)
	if out["instructions"] != "be terse\n\nno emoji" || out["store"] != false || out["max_output_tokens"] != nil {
		t.Fatalf("body = %v", out)
	}
	if input := out["input"].([]any); len(input) != 1 || input[0].(map[string]any)["role"] != "user" {
		t.Fatalf("input = %v", out["input"])
	}
}

func TestCodexBodyKeepsGivenInstructions(t *testing.T) {
	out := codexed(t, `{"instructions":"mine","input":[{"role":"developer","content":"x"},{"role":"user","content":"hi"}]}`)
	if out["instructions"] != "mine" || len(out["input"].([]any)) != 2 {
		t.Fatalf("body = %v", out)
	}
}

func TestCodexBodyListsAStringInput(t *testing.T) {
	out := codexed(t, `{"input":"hi"}`)
	input := out["input"].([]any)
	if len(input) != 1 || input[0].(map[string]any)["content"] != "hi" || input[0].(map[string]any)["role"] != "user" {
		t.Fatalf("input = %v", out["input"])
	}
}
