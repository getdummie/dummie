package agent

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	maxSessions   = 200
	maxTitleRunes = 120
	// Lines this long are messages carrying images or big tool output; the
	// lines a listing needs are all far smaller.
	maxScanLine = 64 << 10
)

type sessionHeader struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
}

type sessionSummary struct {
	Key       string `json:"key"`
	Harness   string `json:"harness"`
	ID        string `json:"id"`
	Cwd       string `json:"cwd"`
	Name      string `json:"name,omitempty"`
	Title     string `json:"title,omitempty"`
	Created   string `json:"created"`
	Updated   int64  `json:"updated"`
	Live      bool   `json:"live"`
	Streaming bool   `json:"streaming"`
}

type cachedSummary struct {
	mod  time.Time
	size int64
	s    sessionSummary
}

// A file is only read again once it changes. Failures are not kept, since a
// gemini chat's directory may only become known later.
var summaryCache = struct {
	sync.Mutex
	m map[string]map[string]cachedSummary
}{m: map[string]map[string]cachedSummary{}}

func summarizeAll(harness string, files []string, fn func(string, os.FileInfo) (sessionSummary, error)) []sessionSummary {
	summaryCache.Lock()
	prev := summaryCache.m[harness]
	summaryCache.Unlock()
	next := make(map[string]cachedSummary, len(files))
	out := make([]sessionSummary, 0, len(files))
	for _, f := range files {
		fi, err := os.Stat(f)
		if err != nil {
			continue
		}
		c, ok := prev[f]
		if !ok || !c.mod.Equal(fi.ModTime()) || c.size != fi.Size() {
			s, err := fn(f, fi)
			if err != nil {
				continue
			}
			s.Harness, s.Key = harness, sessionKey(harness, s.ID)
			c = cachedSummary{mod: fi.ModTime(), size: fi.Size(), s: s}
		}
		next[f] = c
		out = append(out, c.s)
	}
	summaryCache.Lock()
	summaryCache.m[harness] = next
	summaryCache.Unlock()
	return out
}

func piDir() string {
	if d := os.Getenv("PI_CODING_AGENT_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pi", "agent")
}

func piSessionDir() string {
	if d := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); d != "" {
		return d
	}
	return filepath.Join(piDir(), "sessions")
}

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func findPiSession(id string) (string, error) {
	if !sessionIDPattern.MatchString(id) {
		return "", errors.New("not a pi session id")
	}
	files, _ := filepath.Glob(filepath.Join(piSessionDir(), "*", "*_"+id+".jsonl"))
	for _, f := range files {
		if h, err := readHeader(f); err == nil && h.ID == id {
			return f, nil
		}
	}
	return "", errors.New("no pi session " + id + " in this vm")
}

func readHeader(file string) (sessionHeader, error) {
	f, err := os.Open(file)
	if err != nil {
		return sessionHeader{}, err
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return sessionHeader{}, err
	}
	var h sessionHeader
	if err := json.Unmarshal(bytes.TrimSpace(line), &h); err != nil || h.Type != "session" {
		return sessionHeader{}, errors.New(file + " is not a pi session")
	}
	return h, nil
}

func piSessions() []sessionSummary {
	files, _ := filepath.Glob(filepath.Join(piSessionDir(), "*", "*.jsonl"))
	return summarizeAll("pi", files, summarize)
}

// sessions merges stored sessions with live ones; a just-started session may
// have no file yet and is known only by its process.
func (d *daemon) sessions() []sessionSummary {
	var out []sessionSummary
	for _, h := range d.harnesses {
		if available(h) == nil {
			out = append(out, h.list(d)...)
		}
	}

	d.mu.Lock()
	seen := map[string]bool{}
	for i := range out {
		if p, ok := d.procs[out[i].Key]; ok {
			out[i].Live, out[i].Streaming = true, p.isStreaming()
			if n := p.name(); n != "" {
				out[i].Name = n
			}
			seen[out[i].Key] = true
		}
	}
	for key, p := range d.procs {
		if !seen[key] {
			out = append(out, sessionSummary{Key: key, Harness: p.harness(), ID: p.id(), Cwd: p.cwd(), Name: p.name(), Live: true,
				Streaming: p.isStreaming(), Updated: p.lastUsed().UnixMilli()})
		}
	}
	d.mu.Unlock()

	slices.SortFunc(out, func(a, b sessionSummary) int { return cmp.Compare(b.Updated, a.Updated) })
	if len(out) > maxSessions {
		out = out[:maxSessions]
	}
	return out
}

func summarize(file string, fi os.FileInfo) (sessionSummary, error) {
	f, err := os.Open(file)
	if err != nil {
		return sessionSummary{}, err
	}
	defer f.Close()

	br := bufio.NewReaderSize(f, 64<<10)
	var s sessionSummary
	first := true
	for {
		line, err := readLine(br)
		if len(line) > 0 {
			if first {
				var h sessionHeader
				if json.Unmarshal(line, &h) != nil || h.Type != "session" {
					return sessionSummary{}, errors.New("not a pi session")
				}
				s = sessionSummary{ID: h.ID, Cwd: h.Cwd, Created: h.Timestamp, Updated: fi.ModTime().UnixMilli()}
				first = false
			} else {
				scanEntry(line, &s)
			}
		}
		if err != nil {
			break
		}
	}
	if first {
		return sessionSummary{}, errors.New("empty session")
	}
	return s, nil
}

// readLine returns nil for a line over maxScanLine, after skipping it.
func readLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	over := false
	for {
		frag, err := br.ReadSlice('\n')
		if !over {
			buf = append(buf, frag...)
			if len(buf) > maxScanLine {
				over, buf = true, nil
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimSpace(buf), err
	}
}

func scanEntry(line []byte, s *sessionSummary) {
	var e struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &e) != nil {
		return
	}
	switch {
	case e.Type == "session_info":
		s.Name = e.Name
	case e.Type == "message" && e.Message.Role == "user" && s.Title == "":
		s.Title = clip(userText(e.Message.Content))
	}
}

func userText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			return b.Text
		}
	}
	return ""
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > maxTitleRunes {
		return string(r[:maxTitleRunes]) + "…"
	}
	return s
}
