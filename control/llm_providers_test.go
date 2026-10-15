package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenCodeProviderIsExposedWithoutInternalRouting(t *testing.T) {
	items := toLLMKeyDTOs(nil)
	var got *llmKeyDTO
	for i := range items {
		if items[i].Provider == "opencode-go" {
			got = &items[i]
			break
		}
	}
	if got == nil {
		t.Fatal("opencode-go provider is missing")
	}
	if got.Label != "OpenCode Go" || got.KeyURL != "https://opencode.ai/auth" || len(got.Plans) != 1 ||
		got.Plans[0].ID != "go" || got.Plans[0].Label != "Go / Go Plus" {
		t.Fatalf("provider = %+v", got)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "opencode.ai/zen") || strings.Contains(string(raw), "x-api-key") {
		t.Fatalf("internal routing leaked into response: %s", raw)
	}
}

func TestCheckLLMKeyUsesAuthenticatedPlanEndpoint(t *testing.T) {
	old := llmHTTPClient
	t.Cleanup(func() { llmHTTPClient = old })

	for _, tc := range []struct {
		name   string
		status int
		want   error
	}{
		{name: "accepted", status: http.StatusOK},
		{name: "bad key", status: http.StatusUnauthorized, want: errLLMKeyRejected},
		{name: "no subscription", status: http.StatusForbidden, want: errLLMKeyRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotAuth, gotMethod string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				gotMethod = r.Method
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			llmHTTPClient = srv.Client()

			err := checkLLMKey(context.Background(), llmPlan{KeyCheckURL: srv.URL}, "go-key")
			if err != tc.want {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if gotAuth != "Bearer go-key" {
				t.Fatalf("Authorization = %q", gotAuth)
			}
			if gotMethod != http.MethodGet {
				t.Fatalf("method = %q", gotMethod)
			}
		})
	}
}
