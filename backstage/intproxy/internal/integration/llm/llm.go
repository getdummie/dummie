// Package llm fronts hosted model apis behind one vhost. The model field names
// both the upstream model and whose key pays for it: "zai/glm-4.6" is the
// caller's own key, "zai@global/glm-4.6" the one an admin set for everyone.
// A chatgpt subscription is only served as /v1/responses.
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
	kindResponses = "responses"

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
	case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
		return withModel(r, integration.Route{Kind: kindResponses, UpstreamPath: "/responses", Write: true})
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
	// Only global keys are metered, and an openai stream carries no usage
	// unless it is asked for.
	if route.Kind == kindOpenAI && strings.HasSuffix(source, "@global") && string(body["stream"]) == "true" {
		if _, set := body["stream_options"]; !set {
			body["stream_options"] = json.RawMessage(`{"include_usage":true}`)
		}
	}
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

func (l *LLM) Apply(pr *httputil.ProxyRequest, route integration.Route, tok credential.Token) {
	pr.Out.Header.Set("Authorization", "Bearer "+tok.Value)
	// A chatgpt subscription's backend wants the account and codex's own headers.
	if tok.Account != "" {
		pr.Out.Header.Set("Chatgpt-Account-Id", tok.Account)
		pr.Out.Header.Set("OpenAI-Beta", "responses=experimental")
		pr.Out.Header.Set("Originator", "codex_cli_rs")
		if route.Kind == kindResponses {
			codexBody(pr.Out)
		}
	}
	// Usage is read off the body in flight, which a compressed one defeats.
	pr.Out.Header.Del("Accept-Encoding")
}

// codexBody reshapes a responses request into what the codex backend accepts;
// a body it cannot make sense of goes up as is, for upstream to refuse.
func codexBody(out *http.Request) {
	if out.Body == nil {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(out.Body, maxBody+1))
	_ = out.Body.Close()
	out.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil || len(raw) > maxBody {
		return
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil {
		return
	}

	var input []map[string]json.RawMessage
	var text string
	if json.Unmarshal(body["input"], &text) == nil {
		msg, _ := json.Marshal(text)
		input = []map[string]json.RawMessage{{"role": json.RawMessage(`"user"`), "content": msg}}
	} else if json.Unmarshal(body["input"], &input) != nil {
		return
	}

	var instructions string
	_ = json.Unmarshal(body["instructions"], &instructions)
	if instructions == "" {
		var lead []string
		for len(input) > 0 {
			t, ok := systemText(input[0])
			if !ok {
				break
			}
			lead = append(lead, t)
			input = input[1:]
		}
		if len(lead) > 0 {
			body["instructions"], _ = json.Marshal(strings.Join(lead, "\n\n"))
		}
	}

	body["input"], _ = json.Marshal(input)
	body["store"] = json.RawMessage("false")
	// A subscription has no output cap to set, and the backend refuses the field.
	delete(body, "max_output_tokens")
	next, err := json.Marshal(body)
	if err != nil {
		return
	}
	out.Body = io.NopCloser(bytes.NewReader(next))
	out.ContentLength = int64(len(next))
	out.Header.Set("Content-Length", strconv.Itoa(len(next)))
}

// systemText is the text of a system or developer message, if item is one
// made only of text.
func systemText(item map[string]json.RawMessage) (string, bool) {
	var role string
	if json.Unmarshal(item["role"], &role) != nil || (role != "system" && role != "developer") {
		return "", false
	}
	var s string
	if json.Unmarshal(item["content"], &s) == nil {
		return s, true
	}
	var parts []struct {
		Text *string `json:"text"`
	}
	if json.Unmarshal(item["content"], &parts) != nil {
		return "", false
	}
	texts := make([]string, 0, len(parts))
	for _, p := range parts {
		if p.Text == nil {
			return "", false
		}
		texts = append(texts, *p.Text)
	}
	return strings.Join(texts, "\n"), true
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
	return `intproxy: this host serves GET /v1/models, POST /v1/chat/completions, POST /v1/messages and POST /v1/responses; ` +
		`the body's model must be "<provider>/<model>" for your own key or "<provider>@global/<model>" for the shared one`
}
