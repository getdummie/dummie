package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
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
	File      string `json:"file"`
	ID        string `json:"id"`
	Cwd       string `json:"cwd"`
	Name      string `json:"name,omitempty"`
	Title     string `json:"title,omitempty"`
	Created   string `json:"created"`
	Updated   int64  `json:"updated"`
	Harness   string `json:"harness"`
	Live      bool   `json:"live"`
	Streaming bool   `json:"streaming"`
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

// findSession maps a pi session id to its file: a running session first, since
// pi writes the file only once there is a message in it.
func (d *daemon) findSession(id string) (string, error) {
	if !sessionIDPattern.MatchString(id) {
		return "", errors.New("not a pi session id")
	}
	d.mu.Lock()
	for key, p := range d.procs {
		if p.id == id {
			d.mu.Unlock()
			return key, nil
		}
	}
	d.mu.Unlock()
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

func (d *daemon) sessions() []sessionSummary {
	files, _ := filepath.Glob(filepath.Join(piSessionDir(), "*", "*.jsonl"))
	out := make([]sessionSummary, 0, len(files))
	for _, f := range files {
		s, err := summarize(f)
		if err != nil {
			continue
		}
		out = append(out, s)
	}

	d.mu.Lock()
	seen := map[string]bool{}
	for i := range out {
		if p, ok := d.procs[out[i].File]; ok {
			out[i].Live, out[i].Streaming = true, p.isStreaming()
			seen[out[i].File] = true
		}
	}
	// pi writes a session file only once it has a message, so a session that
	// was just started is known only by its process.
	for key, p := range d.procs {
		if !seen[key] {
			out = append(out, sessionSummary{File: key, ID: p.id, Cwd: p.cwd, Harness: "pi", Live: true,
				Streaming: p.isStreaming(), Updated: p.lastUsed().UnixMilli()})
		}
	}
	d.mu.Unlock()

	slices.SortFunc(out, func(a, b sessionSummary) int { return int(b.Updated - a.Updated) })
	if len(out) > maxSessions {
		out = out[:maxSessions]
	}
	return out
}

func summarize(file string) (sessionSummary, error) {
	fi, err := os.Stat(file)
	if err != nil {
		return sessionSummary{}, err
	}
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
				s = sessionSummary{File: file, ID: h.ID, Cwd: h.Cwd, Created: h.Timestamp,
					Updated: fi.ModTime().UnixMilli(), Harness: "pi"}
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
