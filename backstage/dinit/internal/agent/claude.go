package agent

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// claudeAliases are what claude takes for --model on its own login.
var claudeAliases = []string{"default", "sonnet", "opus", "haiku"}

type claudeHarness struct{}

func (*claudeHarness) name() string   { return "claude" }
func (*claudeHarness) binary() string { return "claude" }
func (*claudeHarness) steers() bool   { return false }
func (*claudeHarness) renames() bool  { return false }

func (*claudeHarness) presetModels() []listedModel {
	if px, ok := loadProxy(); ok && len(px.chatModels()) > 0 {
		return listed(providerChat, px.chatModels())
	}
	return listed("anthropic", claudeAliases)
}

func claudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func claudeFiles() []string {
	files, _ := filepath.Glob(filepath.Join(claudeDir(), "projects", "*", "*.jsonl"))
	out := files[:0]
	for _, f := range files {
		// Subagents keep their own transcripts next to the sessions.
		if !strings.HasPrefix(filepath.Base(f), "agent-") {
			out = append(out, f)
		}
	}
	return out
}

func findClaudeSession(id string) (string, error) {
	files, _ := filepath.Glob(filepath.Join(claudeDir(), "projects", "*", id+".jsonl"))
	if len(files) == 0 {
		return "", errors.New("no claude session " + id + " in this vm")
	}
	return files[0], nil
}

func (*claudeHarness) list(*daemon) []sessionSummary {
	return summarizeAll("claude", claudeFiles(), summarizeClaude)
}

type claudeRecord struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	SessionID   string `json:"sessionId"`
	Cwd         string `json:"cwd"`
	Timestamp   string `json:"timestamp"`
	Summary     string `json:"summary"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func summarizeClaude(file string, fi os.FileInfo) (sessionSummary, error) {
	f, err := os.Open(file)
	if err != nil {
		return sessionSummary{}, err
	}
	defer f.Close()
	s := sessionSummary{ID: strings.TrimSuffix(filepath.Base(file), ".jsonl"), Updated: fi.ModTime().UnixMilli()}
	br := bufio.NewReaderSize(f, 64<<10)
	for s.Title == "" {
		line, err := readLine(br)
		var r claudeRecord
		if len(line) > 0 && json.Unmarshal(line, &r) == nil && !r.IsSidechain {
			switch {
			case r.Type == "summary" && s.Name == "":
				s.Name = clip(r.Summary)
			case r.Type == "user" && !r.IsMeta:
				if s.Cwd == "" {
					s.Cwd, s.Created = r.Cwd, r.Timestamp
				}
				if t := userText(r.Message.Content); t != "" && !strings.HasPrefix(t, "<") {
					s.Title = clip(t)
				}
			}
		}
		if err != nil {
			break
		}
	}
	if s.Cwd == "" {
		return sessionSummary{}, errors.New("no prompt in this session")
	}
	return s, nil
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (h *claudeHarness) start(d *daemon, id, cwd string, model *modelRef) (proc, error) {
	file := ""
	if id != "" {
		f, err := findClaudeSession(id)
		if err != nil {
			return nil, err
		}
		fi, err := os.Stat(f)
		if err != nil {
			return nil, err
		}
		s, err := summarizeClaude(f, fi)
		if err != nil {
			return nil, err
		}
		file, cwd = f, s.Cwd
	}
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages", "--replay-user-messages", "--permission-mode", "bypassPermissions"}
	p := &claudeProc{base: newBase("claude", cwd), d: d, file: file}
	if id != "" {
		p.sessionID = id
		args = append(args, "--resume", id)
	} else {
		p.sessionID = newUUID()
		args = append(args, "--session-id", p.sessionID)
	}

	env := os.Environ()
	if px, ok := loadProxy(); ok && len(px.chatModels()) > 0 {
		// The proxy names models by key source, so claude's own defaults would
		// not resolve; every slot it fills on its own gets the chosen model.
		p.proxied = px.chatModels()
		m := pickModel(model, p.proxied)
		p.current = &modelRef{Provider: providerChat, ID: m}
		args = append(args, "--model", m)
		env = append(withoutEnv("ANTHROPIC_API_KEY"),
			"ANTHROPIC_BASE_URL="+strings.TrimSuffix(px.Base, "/v1"), "ANTHROPIC_AUTH_TOKEN="+placeholderKey,
			"ANTHROPIC_DEFAULT_HAIKU_MODEL="+m, "ANTHROPIC_DEFAULT_SONNET_MODEL="+m,
			"ANTHROPIC_DEFAULT_OPUS_MODEL="+m, "CLAUDE_CODE_SUBAGENT_MODEL="+m)
	} else if model != nil && model.ID != "default" {
		p.current = &modelRef{Provider: "anthropic", ID: model.ID}
		args = append(args, "--model", model.ID)
	}
	// claude refuses to skip permission prompts as root outside a sandbox,
	// and a vm is one.
	if os.Geteuid() == 0 {
		env = append(env, "IS_SANDBOX=1")
	}

	cmd := exec.Command("claude", args...)
	cmd.Dir, cmd.Env = cwd, env
	p.io = newStdio(cmd)
	if err := p.io.start(p.handle, func(err error) {
		<-p.ready
		d.onExit(p, err)
	}); err != nil {
		return nil, err
	}
	// initialize is what claude's sdk sends first; its answer lists models, and
	// a claude too old to know it still works without.
	res, err := p.control(map[string]any{"subtype": "initialize"}, 15*time.Second)
	close(p.ready)
	select {
	case <-p.io.done:
		return nil, fmt.Errorf("claude did not start: %s", p.io.stderr.String())
	default:
	}
	if err != nil {
		log.Printf("agent: claude initialize: %v", err)
	}
	var caps struct {
		Models []struct {
			Value       string `json:"value"`
			DisplayName string `json:"displayName"`
		} `json:"models"`
	}
	if json.Unmarshal(res, &caps) == nil {
		for _, m := range caps.Models {
			p.known = append(p.known, listedModel{Provider: "anthropic", ID: m.Value, Name: m.DisplayName})
		}
	}
	return p, nil
}

// claudeProc is one `claude -p` in stream-json mode. Prompts sent mid-turn
// queue behind it, so busy counts the turns not yet answered.
type claudeProc struct {
	*base
	d    *daemon
	io   *stdio
	file string

	proxied []string
	known   []listedModel
	current *modelRef
	turns   int
}

type claudeLine struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	Model     string          `json:"model"`
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
	Response  struct {
		Subtype   string          `json:"subtype"`
		RequestID string          `json:"request_id"`
		Response  json.RawMessage `json:"response"`
		Error     string          `json:"error"`
	} `json:"response"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func (p *claudeProc) handle(line []byte) {
	var l claudeLine
	if json.Unmarshal(line, &l) != nil {
		return
	}
	switch l.Type {
	case "control_response":
		p.io.resolve(l.Response.RequestID, json.RawMessage(line))
		return
	case "control_request":
		go p.allow(l.RequestID, l.Request)
		return
	}
	sig := sigNone
	switch {
	case l.Type == "system" && l.Subtype == "init" && l.Model != "":
		p.mu.Lock()
		prov := "anthropic"
		if p.proxied != nil {
			prov = providerChat
		}
		p.current = &modelRef{Provider: prov, ID: l.Model}
		p.mu.Unlock()
	case l.Type == "result":
		p.mu.Lock()
		p.turns = max(0, p.turns-1)
		busy := p.turns > 0
		p.mu.Unlock()
		p.setBusy(busy)
		sig = sigSettled
	case l.Type == "user" && bytes.Contains(l.Message.Content, []byte(`"tool_result"`)):
		sig = sigTouched
	}
	p.touch()
	if p.isReady() {
		p.d.onEvent(p, json.RawMessage(line), sig)
	}
}

// allow answers claude's permission questions, which bypassPermissions should
// leave it no reason to ask.
func (p *claudeProc) allow(id string, raw json.RawMessage) {
	var req struct {
		Subtype string          `json:"subtype"`
		Input   json.RawMessage `json:"input"`
	}
	_ = json.Unmarshal(raw, &req)
	resp := map[string]any{"subtype": "error", "request_id": id, "error": "dinit does not handle " + req.Subtype}
	if req.Subtype == "can_use_tool" {
		resp = map[string]any{"subtype": "success", "request_id": id,
			"response": map[string]any{"behavior": "allow", "updatedInput": req.Input}}
	}
	_ = p.io.send(map[string]any{"type": "control_response", "response": resp})
}

func (p *claudeProc) control(req map[string]any, timeout time.Duration) (json.RawMessage, error) {
	id := "d" + strconv.Itoa(p.io.nextID())
	raw, err := p.io.await(id, timeout, func() error {
		return p.io.send(map[string]any{"type": "control_request", "request_id": id, "request": req})
	})
	if err != nil {
		return nil, err
	}
	var l claudeLine
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, err
	}
	if l.Response.Subtype != "success" {
		return nil, errors.New(l.Response.Error)
	}
	return l.Response.Response, nil
}

// history is the transcript's main-thread records; a new session has none
// until its first prompt.
func (p *claudeProc) history() (json.RawMessage, error) {
	file := p.file
	if file == "" {
		f, err := findClaudeSession(p.sessionID)
		if err != nil {
			return json.RawMessage("[]"), nil
		}
		file = f
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []json.RawMessage
	br := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		var r claudeRecord
		if line = bytes.TrimSpace(line); len(line) > 0 && json.Unmarshal(line, &r) == nil &&
			(r.Type == "user" || r.Type == "assistant") && !r.IsSidechain {
			out = append(out, json.RawMessage(line))
		}
		if err != nil {
			break
		}
	}
	if out == nil {
		return json.RawMessage("[]"), nil
	}
	return json.Marshal(out)
}

func (p *claudeProc) model() *modelRef {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.current
}

func (p *claudeProc) prompt(text string, images []image) error {
	content := []map[string]any{}
	for _, i := range images {
		content = append(content, map[string]any{"type": "image",
			"source": map[string]any{"type": "base64", "media_type": i.Mime, "data": i.Data}})
	}
	content = append(content, map[string]any{"type": "text", "text": text})
	p.mu.Lock()
	p.turns++
	p.mu.Unlock()
	p.setBusy(true)
	err := p.io.send(map[string]any{"type": "user", "session_id": p.sessionID, "parent_tool_use_id": nil,
		"message": map[string]any{"role": "user", "content": content}})
	if err != nil {
		return err
	}
	p.d.broadcastSessions()
	return nil
}

func (p *claudeProc) abort() error {
	_, err := p.control(map[string]any{"subtype": "interrupt"}, rpcTimeout)
	return err
}

func (p *claudeProc) models() ([]listedModel, error) {
	switch {
	case p.proxied != nil:
		return listed(providerChat, p.proxied), nil
	case len(p.known) > 0:
		return p.known, nil
	}
	return listed("anthropic", claudeAliases), nil
}

func (p *claudeProc) setModel(m modelRef) error {
	if _, err := p.control(map[string]any{"subtype": "set_model", "model": m.ID}, rpcTimeout); err != nil {
		return err
	}
	p.mu.Lock()
	p.current = &m
	p.mu.Unlock()
	return nil
}

func (p *claudeProc) rename(string) error { return errNoRename }

func (p *claudeProc) stop() { p.io.stop() }
