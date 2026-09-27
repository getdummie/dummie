package intproxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"intproxy/internal/credential"
	"intproxy/internal/integration"
)

func withLLM(t *testing.T, s *Server) string {
	t.Helper()
	ig, err := integration.New("llm", integration.Options{})
	if err != nil {
		t.Fatal(err)
	}
	host := s.cfg.hostname("llm")
	s.hosts[host] = ig
	return host
}

type fakeRelay struct {
	path, ip string
	status   int
	body     string
}

func (f *fakeRelay) Relay(_ context.Context, path, ip string) (int, []byte, error) {
	f.path, f.ip = path, ip
	return f.status, []byte(f.body), nil
}

func TestEndToEndLLMUsesTheBrokersUpstream(t *testing.T) {
	var seen struct{ auth, apiKey, path, model string }
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		seen.auth, seen.apiKey, seen.path, seen.model = r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"), r.URL.Path, body.Model
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer upstream.Close()

	var scope credential.Scope
	src := fakeSource{fn: func(s credential.Scope) (credential.Token, error) {
		scope = s
		return credential.Token{Value: "zai_key", Upstream: upstream.URL + "/api/paas/v4"}, nil
	}}
	s := testServer(t, src, "")
	s.rp.Transport = upstream.Client().Transport
	host := withLLM(t, s)

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"zai@global/glm-4.6"}`))
	r.Host = host
	r.Header.Set("Authorization", "Bearer sk-the-guests-own")
	r.Header.Set("X-Api-Key", "sk-the-guests-own")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", w.Code, w.Body.String())
	}
	if scope.Resource != "zai@global/openai" {
		t.Fatalf("resource = %q", scope.Resource)
	}
	if seen.auth != "Bearer zai_key" || seen.apiKey != "" {
		t.Fatalf("upstream auth = %q, x-api-key = %q", seen.auth, seen.apiKey)
	}
	if seen.path != "/api/paas/v4/chat/completions" || seen.model != "glm-4.6" {
		t.Fatalf("upstream path = %q, model = %q", seen.path, seen.model)
	}
}

func TestEndToEndChatGPTSendsTheBrokersAccount(t *testing.T) {
	var seen struct{ auth, account, beta, path string }
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.auth, seen.account, seen.beta, seen.path = r.Header.Get("Authorization"), r.Header.Get("Chatgpt-Account-Id"), r.Header.Get("OpenAI-Beta"), r.URL.Path
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer upstream.Close()

	src := fakeSource{fn: func(credential.Scope) (credential.Token, error) {
		return credential.Token{Value: "access", Account: "acct-1", Upstream: upstream.URL + "/backend-api/codex"}, nil
	}}
	s := testServer(t, src, "")
	s.rp.Transport = upstream.Client().Transport
	host := withLLM(t, s)

	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"chatgpt/gpt-5-codex"}`))
	r.Host = host
	r.Header.Set("Chatgpt-Account-Id", "acct-the-guests-own")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", w.Code, w.Body.String())
	}
	if seen.auth != "Bearer access" || seen.account != "acct-1" || seen.beta != "responses=experimental" {
		t.Fatalf("upstream headers = %+v", seen)
	}
	if seen.path != "/backend-api/codex/responses" {
		t.Fatalf("upstream path = %q", seen.path)
	}
}

// A source is trusted to name the upstream, but never over plain http.
func TestUpstreamBaseRequiresHTTPS(t *testing.T) {
	for _, raw := range []string{"http://api.z.ai/v4", "https://", "https://u:p@api.z.ai", "https://api.z.ai/v4?x=1"} {
		if _, ok := upstreamBase(raw); ok {
			t.Errorf("%q was accepted", raw)
		}
	}
	if u, ok := upstreamBase("https://api.z.ai/api/paas/v4"); !ok || u.Host != "api.z.ai" {
		t.Fatal("a plain https base was refused")
	}
}

func TestModelsAreRelayedFromTheBroker(t *testing.T) {
	s := testServer(t, denying(http.StatusForbidden, "unused"), "")
	relay := &fakeRelay{status: http.StatusOK, body: `{"object":"list","data":[]}`}
	s.relay = relay
	host := withLLM(t, s)

	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.Host = host
	r.RemoteAddr = "10.64.0.9:5555"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	body, _ := io.ReadAll(w.Body)
	if w.Code != http.StatusOK || string(body) != relay.body {
		t.Fatalf("status = %d, body %q", w.Code, body)
	}
	if relay.path != "/v1/llm/models" || relay.ip != "10.64.0.9" {
		t.Fatalf("relayed %q for %q", relay.path, relay.ip)
	}
}

func TestModelsWithoutABrokerAreNotImplemented(t *testing.T) {
	s := testServer(t, denying(http.StatusForbidden, "unused"), "")
	host := withLLM(t, s)

	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.Host = host
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestMeteredResponsesAreRecorded(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"glm-4.6","usage":{"prompt_tokens":9,"completion_tokens":4}}`))
	}))
	defer upstream.Close()

	src := fakeSource{fn: func(credential.Scope) (credential.Token, error) {
		return credential.Token{Value: "k", Upstream: upstream.URL, Meter: "user/vm/zai"}, nil
	}}
	s := testServer(t, src, "")
	s.rp.Transport = upstream.Client().Transport
	var out strings.Builder
	s.usage = &out
	host := withLLM(t, s)

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"zai@global/glm-4.6"}`))
	r.Host = host
	s.ServeHTTP(httptest.NewRecorder(), r)

	line, ok := strings.CutPrefix(strings.TrimSpace(out.String()), UsageMarker+" ")
	if !ok {
		t.Fatalf("no usage line: %q", out.String())
	}
	var got usageLine
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatal(err)
	}
	if got.Meter != "user/vm/zai" || got.Model != "glm-4.6" || got.Input != 9 || got.Output != 4 || got.Status != 200 {
		t.Fatalf("usage = %+v", got)
	}
}
