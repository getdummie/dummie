package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// A turn has no deadline of its own; this only catches a gemini that hangs.
const geminiTurnTimeout = 24 * time.Hour

type geminiHarness struct{}

func (*geminiHarness) name() string                { return "gemini" }
func (*geminiHarness) binary() string              { return "gemini" }
func (*geminiHarness) steers() bool                { return false }
func (*geminiHarness) renames() bool               { return false }
func (*geminiHarness) presetModels() []listedModel { return nil }

func geminiDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gemini")
}

func geminiCwdsFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dummie", "gemini-cwds")
}

// gemini files chats under a hash of the directory they ran in, so the
// directory is found by hashing every one dinit knows of.
func (*geminiHarness) list(d *daemon) []sessionSummary {
	byHash := map[string]string{}
	add := func(cwd string) {
		if cwd != "" {
			sum := sha256.Sum256([]byte(cwd))
			byHash[hex.EncodeToString(sum[:])] = cwd
		}
	}
	add(d.home)
	add(d.defaultCwd())
	if b, err := os.ReadFile(geminiCwdsFile()); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			add(l)
		}
	}
	for _, s := range piSessions() {
		add(s.Cwd)
	}
	for _, s := range summarizeAll("claude", claudeFiles(), summarizeClaude) {
		add(s.Cwd)
	}

	files, _ := filepath.Glob(filepath.Join(geminiDir(), "tmp", "*", "chats", "session-*.json"))
	return summarizeAll("gemini", files, func(f string, fi os.FileInfo) (sessionSummary, error) {
		project := filepath.Dir(filepath.Dir(f))
		cwd := byHash[filepath.Base(project)]
		if cwd == "" {
			if b, err := os.ReadFile(filepath.Join(project, ".project_root")); err == nil {
				cwd = strings.TrimSpace(string(b))
			}
		}
		if cwd == "" {
			return sessionSummary{}, errors.New("unknown directory")
		}
		return summarizeGemini(f, fi, cwd)
	})
}

func summarizeGemini(file string, fi os.FileInfo, cwd string) (sessionSummary, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return sessionSummary{}, err
	}
	var chat struct {
		SessionID string `json:"sessionId"`
		StartTime string `json:"startTime"`
		Messages  []struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(b, &chat); err != nil || chat.SessionID == "" {
		return sessionSummary{}, errors.New("not a gemini chat")
	}
	s := sessionSummary{ID: chat.SessionID, Cwd: cwd, Created: chat.StartTime, Updated: fi.ModTime().UnixMilli()}
	for _, m := range chat.Messages {
		if m.Type == "user" {
			s.Title = clip(userText(m.Content))
			break
		}
	}
	return s, nil
}

func rememberGeminiCwd(cwd string) {
	b, _ := os.ReadFile(geminiCwdsFile())
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if slices.Contains(lines, cwd) {
		return
	}
	f, err := os.OpenFile(geminiCwdsFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(cwd + "\n")
}

func (h *geminiHarness) start(d *daemon, id, cwd string, model *modelRef) (proc, error) {
	if id != "" {
		for _, s := range h.list(d) {
			if s.ID == id {
				cwd = s.Cwd
			}
		}
		if cwd == "" {
			return nil, errors.New("no gemini session " + id + " in this vm")
		}
	}
	cmd := exec.Command("gemini", "--experimental-acp", "--yolo")
	cmd.Dir = cwd
	p := &geminiProc{base: newBase("gemini", cwd), d: d}
	p.rpc = &jsonrpc{stdio: newStdio(cmd), tagged: true, onNotify: p.notified, onRequest: p.requested}
	if err := p.rpc.start(p.rpc.handle, func(err error) {
		<-p.ready
		d.onExit(p, err)
	}); err != nil {
		return nil, err
	}
	err := p.begin(id, cwd)
	close(p.ready)
	if err != nil {
		p.stop()
		return nil, fmt.Errorf("gemini did not start: %w", err)
	}
	if id == "" {
		rememberGeminiCwd(cwd)
		if model != nil {
			if err := p.setModel(*model); err != nil {
				log.Printf("agent: gemini kept its default model: %v", err)
			}
		}
	}
	return p, nil
}

// geminiProc is one `gemini --experimental-acp` session. acp has no history
// call, so the load replay and every event since are kept as history.
type geminiProc struct {
	*base
	d   *daemon
	rpc *jsonrpc

	loading bool
	log     []json.RawMessage
	avail   []listedModel
	current *modelRef
	queue   [][]map[string]any
}

type acpModels struct {
	Models *struct {
		Available []struct {
			ModelID string `json:"modelId"`
			Name    string `json:"name"`
		} `json:"availableModels"`
		Current string `json:"currentModelId"`
	} `json:"models"`
}

func (p *geminiProc) begin(id, cwd string) error {
	res, err := p.rpc.call("initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{"fs": map[string]bool{"readTextFile": false, "writeTextFile": false}, "terminal": false},
	}, 30*time.Second)
	if err != nil {
		return err
	}
	var caps struct {
		AgentCapabilities struct {
			LoadSession bool `json:"loadSession"`
		} `json:"agentCapabilities"`
	}
	_ = json.Unmarshal(res, &caps)

	params := map[string]any{"cwd": cwd, "mcpServers": []any{}}
	if id == "" {
		res, err = p.rpc.call("session/new", params, rpcTimeout)
	} else {
		if !caps.AgentCapabilities.LoadSession {
			return errors.New("this gemini cannot reopen a session; update it")
		}
		params["sessionId"] = id
		p.mu.Lock()
		p.loading = true
		p.mu.Unlock()
		res, err = p.rpc.call("session/load", params, rpcTimeout)
		p.mu.Lock()
		p.loading = false
		p.mu.Unlock()
	}
	if err != nil {
		return err
	}
	var s struct {
		SessionID string `json:"sessionId"`
		acpModels
	}
	if err := json.Unmarshal(res, &s); err != nil {
		return err
	}
	p.sessionID = s.SessionID
	if id != "" {
		p.sessionID = id
	}
	if p.sessionID == "" {
		return errors.New("gemini did not report a session id")
	}
	if s.Models != nil {
		for _, m := range s.Models.Available {
			p.avail = append(p.avail, listedModel{Provider: "google", ID: m.ModelID, Name: m.Name})
		}
		if s.Models.Current != "" {
			p.current = &modelRef{Provider: "google", ID: s.Models.Current}
		}
	}
	return nil
}

type acpUpdate struct {
	Params struct {
		Update struct {
			SessionUpdate string `json:"sessionUpdate"`
			Status        string `json:"status"`
		} `json:"update"`
	} `json:"params"`
}

func (p *geminiProc) notified(method string, line []byte) {
	if method != "session/update" {
		return
	}
	p.mu.Lock()
	p.log = append(p.log, json.RawMessage(line))
	loading := p.loading
	p.mu.Unlock()
	if loading {
		return
	}
	var u acpUpdate
	_ = json.Unmarshal(line, &u)
	sig := sigNone
	if u.Params.Update.SessionUpdate == "tool_call_update" && u.Params.Update.Status == "completed" {
		sig = sigTouched
	}
	p.touch()
	if p.isReady() {
		p.d.onEvent(p, json.RawMessage(line), sig)
	}
}

// emit is an event dinit makes up: acp says nothing of a turn starting or of
// the prompt that started it.
func (p *geminiProc) emit(v any, sig signal) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	p.mu.Lock()
	p.log = append(p.log, b)
	p.mu.Unlock()
	p.d.onEvent(p, b, sig)
}

// requested allows every tool gemini asks about; --yolo should leave it none.
func (p *geminiProc) requested(method string, params json.RawMessage) (any, error) {
	if method != "session/request_permission" {
		return nil, fmt.Errorf("dinit does not handle %s", method)
	}
	var req struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(params, &req)
	for _, kind := range []string{"allow_always", "allow_once"} {
		for _, o := range req.Options {
			if o.Kind == kind {
				return map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": o.OptionID}}, nil
			}
		}
	}
	return map[string]any{"outcome": map[string]string{"outcome": "cancelled"}}, nil
}

func (p *geminiProc) history() (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.log) == 0 {
		return json.RawMessage("[]"), nil
	}
	return json.Marshal(p.log)
}

func (p *geminiProc) model() *modelRef {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.current
}

func (p *geminiProc) prompt(text string, images []image) error {
	blocks := []map[string]any{{"type": "text", "text": text}}
	for _, i := range images {
		blocks = append(blocks, map[string]any{"type": "image", "data": i.Data, "mimeType": i.Mime})
	}
	p.mu.Lock()
	if p.busy {
		p.queue = append(p.queue, blocks)
		p.mu.Unlock()
		return nil
	}
	p.busy = true
	p.mu.Unlock()
	go p.turn(blocks)
	return nil
}

// turn runs prompts until the queue is empty; session/prompt answers only
// once the turn is over.
func (p *geminiProc) turn(blocks []map[string]any) {
	for blocks != nil {
		for _, b := range blocks {
			p.emit(map[string]any{"method": "session/update", "params": map[string]any{"sessionId": p.sessionID,
				"update": map[string]any{"sessionUpdate": "user_message_chunk", "content": b}}}, sigNone)
		}
		p.emit(map[string]any{"method": "dummie/turn_start"}, sigBusy)
		res, err := p.rpc.call("session/prompt", map[string]any{"sessionId": p.sessionID, "prompt": blocks}, geminiTurnTimeout)
		var out struct {
			StopReason string `json:"stopReason"`
		}
		_ = json.Unmarshal(res, &out)
		end := map[string]any{"stopReason": out.StopReason}
		if err != nil {
			end["error"] = err.Error()
		}

		p.mu.Lock()
		blocks = nil
		if len(p.queue) > 0 {
			blocks, p.queue = p.queue[0], p.queue[1:]
		}
		p.busy = blocks != nil
		p.used = time.Now()
		p.mu.Unlock()
		p.emit(map[string]any{"method": "dummie/turn_end", "params": end}, sigSettled)
	}
}

// abort cancels the running turn, which then ends with stopReason cancelled,
// and drops whatever was queued behind it.
func (p *geminiProc) abort() error {
	p.mu.Lock()
	p.queue = nil
	p.mu.Unlock()
	return p.rpc.notify("session/cancel", map[string]any{"sessionId": p.sessionID})
}

func (p *geminiProc) models() ([]listedModel, error) {
	return p.avail, nil
}

func (p *geminiProc) setModel(m modelRef) error {
	if len(p.avail) == 0 {
		return errors.New("this gemini does not let a client pick its model")
	}
	if _, err := p.rpc.call("session/set_model", map[string]any{"sessionId": p.sessionID, "modelId": m.ID}, rpcTimeout); err != nil {
		return err
	}
	p.mu.Lock()
	p.current = &modelRef{Provider: "google", ID: m.ID}
	p.mu.Unlock()
	return nil
}

func (p *geminiProc) rename(string) error { return errNoRename }

func (p *geminiProc) stop() { p.rpc.stop() }
