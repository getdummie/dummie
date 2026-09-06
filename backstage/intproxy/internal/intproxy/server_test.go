package intproxy

import (
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serverWithBroker(t *testing.T, b *broker) *Server {
	t.Helper()
	s := &Server{cfg: testConfig(), log: slog.New(slog.DiscardHandler), version: "test", broker: b}
	s.cert.Store(&tls.Certificate{})
	s.rp = newReverseProxy(s)
	return s
}

func clientHello(sni string) *tls.ClientHelloInfo {
	return &tls.ClientHelloInfo{ServerName: sni}
}

func denyingBroker(t *testing.T, status int, msg string) *broker {
	t.Helper()
	return fakeBroker(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(brokerError{Message: msg})
	})
}

func TestServeRejectsForeignHost(t *testing.T) {
	s := serverWithBroker(t, denyingBroker(t, http.StatusForbidden, "nope"))
	r := httptest.NewRequest(http.MethodGet, "/getdummie/dummie/info/refs?service=git-upload-pack", nil)
	r.Host = "llm.int.example.com"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestServeUnknownPath(t *testing.T) {
	s := serverWithBroker(t, denyingBroker(t, http.StatusForbidden, "nope"))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = s.ServerName()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content-type = %q", ct)
	}
}

// git prompts for credentials on a 401, which is the confusing outcome this
// whole feature exists to avoid, so a refusal must never be one.
func TestGitDenialIsNever401(t *testing.T) {
	s := serverWithBroker(t, denyingBroker(t, http.StatusForbidden, "not attached to this vm"))
	r := httptest.NewRequest(http.MethodGet, "/getdummie/dummie/info/refs?service=git-upload-pack", nil)
	r.Host = s.ServerName()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "not attached to this vm") {
		t.Fatalf("body = %q, want the broker's reason", body)
	}
	if !strings.Contains(body, "https://control.example.com/integrations") {
		t.Fatalf("body = %q, want a pointer to the console", body)
	}
}

func TestRESTDenialUsesGitHubErrorShape(t *testing.T) {
	s := serverWithBroker(t, denyingBroker(t, http.StatusForbidden, "not attached"))
	r := httptest.NewRequest(http.MethodGet, "/api/v3/repos/getdummie/dummie", nil)
	r.Host = s.ServerName()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d", w.Code)
	}
	var body struct {
		Message          string `json:"message"`
		DocumentationURL string `json:"documentation_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.Message, "not attached") {
		t.Fatalf("message = %q", body.Message)
	}
	if body.DocumentationURL != "https://control.example.com/integrations" {
		t.Fatalf("documentation_url = %q", body.DocumentationURL)
	}
}

func TestGraphQLDenialUsesErrorsArray(t *testing.T) {
	s := serverWithBroker(t, denyingBroker(t, http.StatusForbidden, "not attached"))
	r := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(`{"query":"{viewer{login}}"}`))
	r.Host = s.ServerName()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	var body struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Errors) != 1 || !strings.Contains(body.Errors[0].Message, "not attached") {
		t.Fatalf("errors = %+v", body.Errors)
	}
}

func TestServerName(t *testing.T) {
	s := serverWithBroker(t, denyingBroker(t, http.StatusForbidden, "x"))
	if got := s.ServerName(); got != "github.int.example.com" {
		t.Fatalf("ServerName = %q", got)
	}
}

func TestGetCertificateRejectsForeignSNI(t *testing.T) {
	s := serverWithBroker(t, denyingBroker(t, http.StatusForbidden, "x"))
	if _, err := s.getCertificate(clientHello("llm.int.example.com")); err == nil {
		t.Fatal("a foreign SNI was accepted")
	}
	if _, err := s.getCertificate(clientHello("github.int.example.com")); err != nil {
		t.Fatalf("our own SNI was rejected: %v", err)
	}
}
