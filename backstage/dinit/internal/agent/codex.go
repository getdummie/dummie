package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const codexProvider = "dummie"

type codexHarness struct{}

func (*codexHarness) name() string   { return "codex" }
func (*codexHarness) binary() string { return "codex" }
func (*codexHarness) steers() bool   { return false }
func (*codexHarness) renames() bool  { return true }

func (*codexHarness) presetModels() []listedModel {
	if px, ok := loadProxy(); ok && len(px.responsesModels()) > 0 {
		return listed(providerResponses, px.responsesModels())
	}
	return nil
}

func codexHome() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

func (*codexHarness) list(*daemon) []sessionSummary {
	files, _ := filepath.Glob(filepath.Join(codexHome(), "sessions", "*", "*", "*", "rollout-*.jsonl"))
	return summarizeAll("codex", files, summarizeCodex)
}

type codexRolloutLine struct {
	Type    string `json:"type"`
	Payload struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Timestamp string `json:"timestamp"`
		Cwd       string `json:"cwd"`
		Message   string `json:"message"`
	} `json:"payload"`
}

// summarizeCodex reads session_meta (uncapped: it holds codex's instructions)
// and the first user_message event.
func summarizeCodex(file string, fi os.FileInfo) (sessionSummary, error) {
	f, err := os.Open(file)
	if err != nil {
		return sessionSummary{}, err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 64<<10)
	first, err := br.ReadBytes('\n')
	var meta codexRolloutLine
	if json.Unmarshal(bytes.TrimSpace(first), &meta) != nil || meta.Type != "session_meta" || meta.Payload.ID == "" {
		return sessionSummary{}, errors.New("not a codex rollout")
	}
	s := sessionSummary{ID: meta.Payload.ID, Cwd: meta.Payload.Cwd, Created: meta.Payload.Timestamp,
		Updated: fi.ModTime().UnixMilli()}
	for err == nil && s.Title == "" {
		var line []byte
		line, err = readLine(br)
		var l codexRolloutLine
		if len(line) > 0 && json.Unmarshal(line, &l) == nil && l.Type == "event_msg" &&
			l.Payload.Type == "user_message" && !strings.HasPrefix(l.Payload.Message, "<") {
			s.Title = clip(l.Payload.Message)
		}
	}
	return s, nil
}

func (h *codexHarness) start(d *daemon, id, cwd string, model *modelRef) (proc, error) {
	if id != "" {
		files, _ := filepath.Glob(filepath.Join(codexHome(), "sessions", "*", "*", "*", "rollout-*-"+id+".jsonl"))
		if len(files) == 0 {
			return nil, errors.New("no codex session " + id + " in this vm")
		}
		fi, err := os.Stat(files[0])
		if err != nil {
			return nil, err
		}
		s, err := summarizeCodex(files[0], fi)
		if err != nil {
			return nil, err
		}
		cwd = s.Cwd
	}

	// The vm is the sandbox, so codex neither asks nor sandboxes itself.
	args := []string{"app-server", "-c", `approval_policy="never"`, "-c", `sandbox_mode="danger-full-access"`}
	env := os.Environ()
	p := &codexProc{base: newBase("codex", cwd), d: d}
	if px, ok := loadProxy(); ok && len(px.responsesModels()) > 0 {
		p.proxied = px.responsesModels()
		args = append(args, "-c", `model_provider="`+codexProvider+`"`,
			"-c", fmt.Sprintf(`model_providers.%s={name=%q,base_url=%q,wire_api="responses",env_key="DUMMIE_LLM_KEY"}`,
				codexProvider, codexProvider, px.Base))
		env = append(env, "DUMMIE_LLM_KEY="+placeholderKey)
		if id == "" {
			p.next = pickModel(model, p.proxied)
		}
	} else if model != nil && id == "" {
		p.next = model.ID
	}

	cmd := exec.Command("codex", args...)
	cmd.Dir, cmd.Env = cwd, env
	p.rpc = &jsonrpc{stdio: newStdio(cmd), onNotify: p.notified, onRequest: p.requested}
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
		return nil, fmt.Errorf("codex did not start: %w", err)
	}
	return p, nil
}

// codexProc is one `codex app-server` holding one thread. codex takes one turn
// at a time, so prompts sent mid-turn wait in queue.
type codexProc struct {
	*base
	d   *daemon
	rpc *jsonrpc

	proxied []string
	current *modelRef
	next    string
	turn    string
	queue   [][]map[string]any
	turns   json.RawMessage
}

type codexThread struct {
	Thread struct {
		ID    string          `json:"id"`
		Turns json.RawMessage `json:"turns"`
	} `json:"thread"`
	Model string `json:"model"`
}

func (p *codexProc) begin(id, cwd string) error {
	_, err := p.rpc.call("initialize", map[string]any{
		"clientInfo": map[string]any{"name": "dummie", "title": "dummie", "version": p.d.version},
	}, 30*time.Second)
	if err != nil {
		return err
	}
	if err := p.rpc.notify("initialized", nil); err != nil {
		return err
	}
	var res json.RawMessage
	if id == "" {
		params := map[string]any{"cwd": cwd}
		if p.next != "" {
			params["model"] = p.next
		}
		res, err = p.rpc.call("thread/start", params, rpcTimeout)
	} else {
		res, err = p.rpc.call("thread/resume", map[string]any{"threadId": id}, rpcTimeout)
	}
	if err != nil {
		return err
	}
	var t codexThread
	if err := json.Unmarshal(res, &t); err != nil {
		return err
	}
	if t.Thread.ID == "" {
		return errors.New("codex did not report a thread id")
	}
	p.sessionID, p.turns, p.next = t.Thread.ID, t.Thread.Turns, ""
	if t.Model != "" {
		p.current = &modelRef{Provider: p.provider(), ID: t.Model}
	}
	return nil
}

func (p *codexProc) provider() string {
	if p.proxied != nil {
		return providerResponses
	}
	return "openai"
}

type codexNote struct {
	Params struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
		Item struct {
			Type string `json:"type"`
		} `json:"item"`
	} `json:"params"`
}

func (p *codexProc) notified(method string, line []byte) {
	// codex repeats every event in its older shape under codex/event/.
	if strings.HasPrefix(method, "codex/event/") {
		return
	}
	var n codexNote
	_ = json.Unmarshal(line, &n)
	sig := sigNone
	switch method {
	case "turn/started":
		p.mu.Lock()
		p.turn = n.Params.Turn.ID
		p.mu.Unlock()
		p.setBusy(true)
		sig = sigBusy
	case "turn/completed":
		p.mu.Lock()
		p.turn = ""
		var next []map[string]any
		if len(p.queue) > 0 {
			next, p.queue = p.queue[0], p.queue[1:]
		}
		p.mu.Unlock()
		p.setBusy(next != nil)
		sig = sigSettled
		if next != nil {
			go func() {
				if err := p.startTurn(next); err != nil {
					log.Printf("agent: codex %s: queued prompt: %v", p.sessionID, err)
				}
			}()
		}
	case "item/completed":
		if n.Params.Item.Type == "commandExecution" || n.Params.Item.Type == "fileChange" {
			sig = sigTouched
		}
	case "thread/name/updated":
		sig = sigRenamed
	}
	p.touch()
	if p.isReady() {
		p.d.onEvent(p, json.RawMessage(line), sig)
	}
}

// requested approves whatever codex still asks: approval_policy never should
// leave it nothing to ask.
func (p *codexProc) requested(method string, _ json.RawMessage) (any, error) {
	switch {
	case strings.HasSuffix(method, "/requestApproval"):
		return map[string]string{"decision": "accept"}, nil
	case method == "execCommandApproval" || method == "applyPatchApproval":
		return map[string]string{"decision": "approved"}, nil
	}
	return nil, fmt.Errorf("dinit does not handle %s", method)
}

// history is the thread's turns, read again so a session opened mid-run
// shows what happened since it was resumed.
func (p *codexProc) history() (json.RawMessage, error) {
	res, err := p.rpc.call("thread/read", map[string]any{"threadId": p.sessionID, "includeTurns": true}, rpcTimeout)
	if err == nil {
		var t codexThread
		if json.Unmarshal(res, &t) == nil && len(t.Thread.Turns) > 0 {
			return t.Thread.Turns, nil
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.turns) == 0 || string(p.turns) == "null" {
		return json.RawMessage("[]"), nil
	}
	return p.turns, nil
}

func (p *codexProc) model() *modelRef {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.current
}

func (p *codexProc) prompt(text string, images []image) error {
	input := []map[string]any{{"type": "text", "text": text}}
	for _, i := range images {
		input = append(input, map[string]any{"type": "image", "url": "data:" + i.Mime + ";base64," + i.Data})
	}
	p.mu.Lock()
	if p.busy {
		p.queue = append(p.queue, input)
		p.mu.Unlock()
		return nil
	}
	p.busy = true
	p.mu.Unlock()
	return p.startTurn(input)
}

func (p *codexProc) startTurn(input []map[string]any) error {
	params := map[string]any{"threadId": p.sessionID, "input": input}
	p.mu.Lock()
	if p.next != "" {
		params["model"] = p.next
	}
	p.mu.Unlock()
	res, err := p.rpc.call("turn/start", params, rpcTimeout)
	if err != nil {
		p.setBusy(false)
		return err
	}
	var t struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(res, &t)
	p.mu.Lock()
	if p.turn == "" {
		p.turn = t.Turn.ID
	}
	p.mu.Unlock()
	p.d.broadcastSessions()
	return nil
}

// abort also drops whatever was queued behind the running turn.
func (p *codexProc) abort() error {
	p.mu.Lock()
	turn := p.turn
	p.queue = nil
	p.mu.Unlock()
	if turn == "" {
		return nil
	}
	_, err := p.rpc.call("turn/interrupt", map[string]any{"threadId": p.sessionID, "turnId": turn}, rpcTimeout)
	return err
}

func (p *codexProc) models() ([]listedModel, error) {
	if p.proxied != nil {
		return listed(providerResponses, p.proxied), nil
	}
	res, err := p.rpc.call("model/list", map[string]any{}, rpcTimeout)
	if err != nil {
		return nil, err
	}
	var list struct {
		Data []struct {
			ID          string `json:"id"`
			Model       string `json:"model"`
			DisplayName string `json:"displayName"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res, &list); err != nil {
		return nil, err
	}
	out := make([]listedModel, 0, len(list.Data))
	for _, m := range list.Data {
		id := m.Model
		if id == "" {
			id = m.ID
		}
		out = append(out, listedModel{Provider: "openai", ID: id, Name: m.DisplayName})
	}
	return out, nil
}

// setModel applies from the next turn on; codex keeps it for the thread.
func (p *codexProc) setModel(m modelRef) error {
	p.mu.Lock()
	p.next, p.current = m.ID, &modelRef{Provider: p.provider(), ID: m.ID}
	p.mu.Unlock()
	return nil
}

func (p *codexProc) rename(name string) error {
	if _, err := p.rpc.call("thread/name/set", map[string]any{"threadId": p.sessionID, "name": name}, rpcTimeout); err != nil {
		return err
	}
	p.setName(name)
	return nil
}

func (p *codexProc) stop() { p.rpc.stop() }
