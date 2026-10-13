package llm

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"intproxy/internal/integration"
)

func metered(t *testing.T, contentType, body string) integration.Usage {
	t.Helper()
	resp := &http.Response{
		Header: http.Header{"Content-Type": {contentType}},
		Body:   io.NopCloser(strings.NewReader(body)),
	}
	var got *integration.Usage
	(&LLM{}).Meter(resp, func(u integration.Usage) { got = &u })

	// Small reads, so events straddle them as they do off the network.
	buf := make([]byte, 7)
	for {
		if _, err := resp.Body.Read(buf); err != nil {
			break
		}
	}
	_ = resp.Body.Close()
	_ = resp.Body.Close()
	if got == nil {
		t.Fatal("done was never called")
	}
	return *got
}

func TestMeterOpenAIJSON(t *testing.T) {
	u := metered(t, "application/json", `{"model":"glm-4.6","usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":30}}}`)
	want := integration.Usage{Model: "glm-4.6", Input: 70, Output: 20, CacheRead: 30}
	if u != want {
		t.Fatalf("usage = %+v, want %+v", u, want)
	}
}

func TestMeterOpenAIStream(t *testing.T) {
	body := "data: {\"model\":\"glm-4.6\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"model\":\"glm-4.6\",\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":5}}\n\n" +
		"data: [DONE]\n\n"
	u := metered(t, "text/event-stream; charset=utf-8", body)
	want := integration.Usage{Model: "glm-4.6", Input: 12, Output: 5}
	if u != want {
		t.Fatalf("usage = %+v, want %+v", u, want)
	}
}

// Anthropic splits usage across message_start and message_delta, and the
// delta's output count is the running total, not an increment.
func TestMeterAnthropicStream(t *testing.T) {
	body := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"model\":\"glm-4.6\",\"usage\":{\"input_tokens\":40,\"cache_read_input_tokens\":300,\"cache_creation_input_tokens\":10,\"output_tokens\":1}}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":55}}\n\n"
	u := metered(t, "text/event-stream", body)
	want := integration.Usage{Model: "glm-4.6", Input: 40, Output: 55, CacheRead: 300, CacheWrite: 10}
	if u != want {
		t.Fatalf("usage = %+v, want %+v", u, want)
	}
}

func TestMeterResponsesStream(t *testing.T) {
	body := "event: response.created\n" +
		"data: {\"type\":\"response.created\",\"response\":{\"model\":\"gpt-5-codex\",\"usage\":null}}\n\n" +
		"event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-5-codex\",\"usage\":{\"input_tokens\":100,\"input_tokens_details\":{\"cached_tokens\":60},\"output_tokens\":9}}}\n\n"
	u := metered(t, "text/event-stream", body)
	want := integration.Usage{Model: "gpt-5-codex", Input: 40, Output: 9, CacheRead: 60}
	if u != want {
		t.Fatalf("usage = %+v, want %+v", u, want)
	}
}

func TestMeterResponsesStreamWithoutContentType(t *testing.T) {
	// The ChatGPT subscription backend omits Content-Type on its SSE response.
	body := "event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-6-sol\",\"usage\":{\"input_tokens\":10,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":6}}}\n\n"
	u := metered(t, "", body)
	want := integration.Usage{Model: "gpt-6-sol", Input: 10, Output: 6}
	if u != want {
		t.Fatalf("usage = %+v, want %+v", u, want)
	}
}

func TestMeterResponsesJSONWithoutContentType(t *testing.T) {
	u := metered(t, "", `{"model":"gpt-6-sol","usage":{"input_tokens":10,"output_tokens":6}}`)
	want := integration.Usage{Model: "gpt-6-sol", Input: 10, Output: 6}
	if u != want {
		t.Fatalf("usage = %+v, want %+v", u, want)
	}
}

func TestMeterDataFirstStreamWithoutContentType(t *testing.T) {
	body := "data: {\"model\":\"glm-4.6\",\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":5}}\n\n"
	u := metered(t, "", body)
	want := integration.Usage{Model: "glm-4.6", Input: 12, Output: 5}
	if u != want {
		t.Fatalf("usage = %+v, want %+v", u, want)
	}
}

func TestMeterResponsesJSON(t *testing.T) {
	u := metered(t, "application/json", `{"model":"gpt-5-codex","usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":0},"output_tokens":3}}`)
	want := integration.Usage{Model: "gpt-5-codex", Input: 10, Output: 3}
	if u != want {
		t.Fatalf("usage = %+v, want %+v", u, want)
	}
}

func TestMeterSkipsCompressedBodies(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{"Content-Encoding": {"gzip"}},
		Body:   io.NopCloser(strings.NewReader("x")),
	}
	called := false
	(&LLM{}).Meter(resp, func(integration.Usage) { called = true })
	if !called {
		t.Fatal("a body that cannot be read still has to be recorded")
	}
}

func TestOpenAIStreamsAskForUsage(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  bool
	}{{"zai@global/glm-4.6", true}, {"zai/glm-4.6", true}} {
		r, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"`+tc.model+`","stream":true}`))
		if _, ok := (&LLM{}).Classify(r); !ok {
			t.Fatal("not classified")
		}
		raw, _ := io.ReadAll(r.Body)
		if got := strings.Contains(string(raw), `"include_usage":true`); got != tc.want {
			t.Errorf("%s: include_usage injected = %v, body %s", tc.model, got, raw)
		}
	}
}
