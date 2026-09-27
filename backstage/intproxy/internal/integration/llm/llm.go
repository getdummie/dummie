// Package llm fronts hosted model apis behind one vhost. The model field names
// both the upstream model and whose key pays for it: "zai/glm-4.6" is the
// caller's own key, "zai@global/glm-4.6" the one an admin set for everyone.
package llm

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strconv"
	"strings"

	"intproxy/internal/credential"
	"intproxy/internal/integration"
)

func init() { integration.Register("llm", New) }

const (
	kindModels    = "models"
	kindOpenAI    = "openai"
	kindAnthropic = "anthropic"

	// RelayModels is the broker path that lists what the caller may use.
	RelayModels = "/v1/llm/models"

	maxBody = 32 << 20
)

var sourcePattern = regexp.MustCompile(`^[a-z0-9]+(@global)?$`)

type LLM struct{}

func New(integration.Options) (integration.Integration, error) { return &LLM{}, nil }

func (l *LLM) Name() string { return "llm" }

func (l *LLM) Classify(r *http.Request) (integration.Route, bool) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		return integration.Route{Kind: kindModels, Relay: RelayModels}, true
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		return withModel(r, integration.Route{Kind: kindOpenAI, UpstreamPath: "/chat/completions", Write: true})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages":
		return withModel(r, integration.Route{Kind: kindAnthropic, UpstreamPath: "/v1/messages", Write: true})
	}
	return integration.Route{}, false
}

// withModel strips the key source off the model, so upstream sees its own name
// for it, and puts the source in the resource, where the broker picks the key.
func withModel(r *http.Request, route integration.Route) (integration.Route, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	_ = r.Body.Close()
	if err != nil || len(raw) > maxBody {
		return route, false
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return route, false
	}
	var model string
	if err := json.Unmarshal(body["model"], &model); err != nil {
		return route, false
	}
	source, name, ok := strings.Cut(model, "/")
	if !ok || name == "" || !sourcePattern.MatchString(source) {
		return route, false
	}

	body["model"], _ = json.Marshal(name)
	out, err := json.Marshal(body)
	if err != nil {
		return route, false
	}
	r.Body = io.NopCloser(bytes.NewReader(out))
	r.ContentLength = int64(len(out))
	r.Header.Set("Content-Length", strconv.Itoa(len(out)))

	route.Resource = source + "/" + route.Kind
	return route, true
}

func (l *LLM) Apply(pr *httputil.ProxyRequest, _ integration.Route, tok credential.Token) {
	pr.Out.Header.Set("Authorization", "Bearer "+tok.Value)
}

func (l *LLM) ModifyResponse(resp *http.Response) error {
	resp.Header.Del("Set-Cookie")
	return nil
}

// RenderError answers in the caller's own api's error shape, which is what
// its sdk knows how to surface.
func (l *LLM) RenderError(r integration.Route, _ int, msg string) (string, []byte) {
	var body []byte
	if r.Kind == kindAnthropic {
		body, _ = json.Marshal(map[string]any{
			"type":  "error",
			"error": map[string]string{"type": "intproxy_error", "message": msg},
		})
	} else {
		body, _ = json.Marshal(map[string]any{
			"error": map[string]string{"type": "intproxy_error", "message": msg},
		})
	}
	return "application/json; charset=utf-8", append(body, '\n')
}

func (l *LLM) Describe() string {
	return `intproxy: this host serves GET /v1/models, POST /v1/chat/completions and POST /v1/messages; ` +
		`the body's model must be "<provider>/<model>" for your own key or "<provider>@global/<model>" for the shared one`
}
