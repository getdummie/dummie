package llm

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"sync"

	"intproxy/internal/integration"
)

// The body is read as it streams past, never buffered ahead of the client. A
// json body is kept up to maxMetered to be parsed once it ends.
const (
	maxMetered = 8 << 20
	maxSSELine = 1 << 20
)

func (l *LLM) Meter(resp *http.Response, done func(integration.Usage)) {
	if resp.Body == nil || resp.Header.Get("Content-Encoding") != "" {
		done(integration.Usage{})
		return
	}
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	resp.Body = &meterBody{rc: resp.Body, sse: ct == "text/event-stream", detectSSE: ct == "", done: done}
}

type meterBody struct {
	rc  io.ReadCloser
	sse bool
	// The ChatGPT Responses endpoint can stream events without Content-Type.
	detectSSE bool
	buf       []byte
	u         integration.Usage
	done      func(integration.Usage)
	once      sync.Once
}

func (m *meterBody) Read(p []byte) (int, error) {
	n, err := m.rc.Read(p)
	if n > 0 {
		m.feed(p[:n])
	}
	return n, err
}

func (m *meterBody) Close() error {
	err := m.rc.Close()
	m.once.Do(func() {
		if !m.sse {
			m.parse(m.buf)
		}
		m.done(m.u)
	})
	return err
}

func (m *meterBody) feed(p []byte) {
	if !m.sse {
		if len(m.buf)+len(p) <= maxMetered {
			m.buf = append(m.buf, p...)
		}
		if !m.detectSSE {
			return
		}
		start := bytes.TrimLeft(m.buf, " \t\r\n")
		dataPrefix, eventPrefix := []byte("data:"), []byte("event:")
		if bytes.HasPrefix(dataPrefix, start) || bytes.HasPrefix(eventPrefix, start) {
			return // The first read may end in the middle of the prefix.
		}
		m.detectSSE = false
		if !bytes.HasPrefix(start, dataPrefix) && !bytes.HasPrefix(start, eventPrefix) {
			return // Keep buffering an unlabelled JSON response.
		}
		m.sse = true
		p = nil // Process the bytes already buffered below.
		if len(m.buf) > maxSSELine {
			m.buf = nil
			return
		}
	}
	m.buf = append(m.buf, p...)
	for {
		i := bytes.IndexByte(m.buf, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSpace(m.buf[:i])
		m.buf = m.buf[i+1:]
		if data, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			m.parse(bytes.TrimSpace(data))
		}
	}
	if len(m.buf) > maxSSELine {
		m.buf = nil
	}
}

type usageFields struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	PromptDetails    *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	InputDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`

	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	CacheRead    int64 `json:"cache_read_input_tokens"`
	CacheCreate  int64 `json:"cache_creation_input_tokens"`
}

type event struct {
	Model   string       `json:"model"`
	Usage   *usageFields `json:"usage"`
	Message *struct {
		Model string       `json:"model"`
		Usage *usageFields `json:"usage"`
	} `json:"message"`
	Response *struct {
		Model string       `json:"model"`
		Usage *usageFields `json:"usage"`
	} `json:"response"`
}

// parse takes one json document: a whole response, or one sse event.
func (m *meterBody) parse(b []byte) {
	if len(b) == 0 || b[0] != '{' {
		return
	}
	var ev event
	if json.Unmarshal(b, &ev) != nil {
		return
	}
	if ev.Message != nil {
		m.model(ev.Message.Model)
		m.apply(ev.Message.Usage)
	}
	if ev.Response != nil {
		m.model(ev.Response.Model)
		m.apply(ev.Response.Usage)
	}
	m.model(ev.Model)
	m.apply(ev.Usage)
}

func (m *meterBody) model(s string) {
	if s != "" && m.u.Model == "" {
		m.u.Model = s
	}
}

// apply normalises both apis onto one shape: Input excludes cached tokens,
// which openai counts inside prompt_tokens and anthropic reports apart.
func (m *meterBody) apply(f *usageFields) {
	if f == nil {
		return
	}
	// The responses api counts cached tokens inside input_tokens.
	if f.InputDetails != nil {
		m.u.Input = max(f.InputTokens-f.InputDetails.CachedTokens, 0)
		m.u.CacheRead = f.InputDetails.CachedTokens
		m.u.Output = f.OutputTokens
		return
	}
	if f.PromptTokens > 0 || f.CompletionTokens > 0 {
		var cached int64
		if f.PromptDetails != nil {
			cached = f.PromptDetails.CachedTokens
		}
		m.u.Input = max(f.PromptTokens-cached, 0)
		m.u.CacheRead = cached
		m.u.Output = f.CompletionTokens
		return
	}
	// Anthropic's counts are cumulative across message_start and
	// message_delta, so the largest seen is the total.
	m.u.Input = max(m.u.Input, f.InputTokens)
	m.u.Output = max(m.u.Output, f.OutputTokens)
	m.u.CacheRead = max(m.u.CacheRead, f.CacheRead)
	m.u.CacheWrite = max(m.u.CacheWrite, f.CacheCreate)
}
