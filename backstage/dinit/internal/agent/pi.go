package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"time"
)

type piHarness struct{}

func (piHarness) name() string                    { return "pi" }
func (piHarness) binary() string                  { return "pi" }
func (piHarness) steers() bool                    { return true }
func (piHarness) renames() bool                   { return true }
func (piHarness) presetModels() []listedModel     { return configuredModels() }
func (piHarness) list(d *daemon) []sessionSummary { return piSessions() }

func (piHarness) start(d *daemon, id, cwd string, model *modelRef) (proc, error) {
	file := ""
	if id != "" {
		f, err := findPiSession(id)
		if err != nil {
			return nil, err
		}
		h, err := readHeader(f)
		if err != nil {
			return nil, err
		}
		file, cwd = f, h.Cwd
	}
	p, err := startPi(d, cwd, file)
	if err != nil {
		return nil, err
	}
	if model != nil && id == "" {
		if err := p.setModel(*model); err != nil {
			log.Printf("agent: pi kept its default model: %v", err)
		}
	}
	return p, nil
}

// piProc is one `pi --mode rpc` child running one session.
type piProc struct {
	*base
	d  *daemon
	io *stdio
}

type piRecord struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type piState struct {
	SessionFile string    `json:"sessionFile"`
	SessionID   string    `json:"sessionId"`
	Model       *modelRef `json:"model"`
}

type piResponse struct {
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func startPi(d *daemon, cwd, file string) (*piProc, error) {
	args := []string{"--mode", "rpc"}
	if file != "" {
		args = append(args, "--session", file)
	}
	cmd := exec.Command("pi", args...)
	cmd.Dir = cwd
	p := &piProc{base: newBase("pi", cwd), d: d, io: newStdio(cmd)}
	err := p.io.start(p.handle, func(err error) {
		<-p.ready
		d.onExit(p, err)
	})
	if err != nil {
		return nil, err
	}

	var st piState
	res, err := p.call(map[string]any{"type": "get_state"}, 30*time.Second)
	if err == nil {
		if err = json.Unmarshal(res, &st); err == nil && st.SessionID == "" {
			err = errors.New("pi did not report a session id")
		}
	}
	p.sessionID = st.SessionID
	close(p.ready)
	if err != nil {
		p.stop()
		return nil, fmt.Errorf("pi did not start: %w", err)
	}
	return p, nil
}

func (p *piProc) handle(line []byte) {
	var rec piRecord
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	if rec.Type == "response" && rec.ID != "" {
		p.io.resolve(rec.ID, json.RawMessage(line))
		return
	}
	sig := sigNone
	switch rec.Type {
	case "agent_start":
		p.setBusy(true)
		sig = sigBusy
	case "agent_settled":
		p.setBusy(false)
		sig = sigSettled
	case "tool_execution_end":
		sig = sigTouched
	case "session_info_changed":
		sig = sigRenamed
	}
	p.touch()
	if p.isReady() {
		p.d.onEvent(p, json.RawMessage(line), sig)
	}
}

// call sends one command and returns pi's data for it, or its error.
func (p *piProc) call(cmd map[string]any, timeout time.Duration) (json.RawMessage, error) {
	id := "d" + strconv.Itoa(p.io.nextID())
	cmd["id"] = id
	p.touch()
	raw, err := p.io.await(id, timeout, func() error { return p.io.send(cmd) })
	if err != nil {
		return nil, err
	}
	var res piResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	if !res.Success {
		return nil, errors.New(res.Error)
	}
	return res.Data, nil
}

func (p *piProc) history() (json.RawMessage, error) {
	data, err := p.call(map[string]any{"type": "get_messages"}, rpcTimeout)
	if err != nil {
		return nil, err
	}
	var m struct {
		Messages json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m.Messages, nil
}

func (p *piProc) model() *modelRef {
	data, err := p.call(map[string]any{"type": "get_state"}, rpcTimeout)
	if err != nil {
		return nil
	}
	var st piState
	if json.Unmarshal(data, &st) != nil {
		return nil
	}
	return st.Model
}

// prompt steers a running turn: pi refuses a plain prompt while streaming.
func (p *piProc) prompt(text string, images []image) error {
	cmd := map[string]any{"type": "prompt", "message": text}
	if len(images) > 0 {
		var imgs []map[string]string
		for _, i := range images {
			imgs = append(imgs, map[string]string{"type": "image", "data": i.Data, "mimeType": i.Mime})
		}
		cmd["images"] = imgs
	}
	if p.isStreaming() {
		cmd["streamingBehavior"] = "steer"
	}
	_, err := p.call(cmd, rpcTimeout)
	return err
}

func (p *piProc) abort() error {
	_, err := p.call(map[string]any{"type": "abort"}, rpcTimeout)
	return err
}

func (p *piProc) models() ([]listedModel, error) {
	data, err := p.call(map[string]any{"type": "get_available_models"}, rpcTimeout)
	if err != nil {
		return nil, err
	}
	var m struct {
		Models []listedModel `json:"models"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m.Models, nil
}

func (p *piProc) setModel(m modelRef) error {
	_, err := p.call(map[string]any{"type": "set_model", "provider": m.Provider, "modelId": m.ID}, rpcTimeout)
	return err
}

func (p *piProc) rename(name string) error {
	if _, err := p.call(map[string]any{"type": "set_session_name", "name": name}, rpcTimeout); err != nil {
		return err
	}
	p.setName(name)
	return nil
}

func (p *piProc) stop() { p.io.stop() }
