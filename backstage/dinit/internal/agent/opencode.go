package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	opencodeStartWait = 30 * time.Second
	opencodeCallWait  = 30 * time.Second
)

var opencodeListening = regexp.MustCompile(`https?://[^\s]+`)

// opencodeHarness keeps one `opencode serve` for every opencode session, as
// opencode's own clients do; a session is only an id on that server.
type opencodeHarness struct {
	startMu sync.Mutex

	mu      sync.Mutex
	srv     *ocServer
	procs   map[string]*ocProc
	streams map[string]bool
	cached  []sessionSummary
	kicked  bool
	used    time.Time
}

func newOpencodeHarness() *opencodeHarness {
	return &opencodeHarness{procs: map[string]*ocProc{}, streams: map[string]bool{}}
}

func (*opencodeHarness) name() string   { return "opencode" }
func (*opencodeHarness) binary() string { return "opencode" }
func (*opencodeHarness) steers() bool   { return false }
func (*opencodeHarness) renames() bool  { return true }

func (*opencodeHarness) presetModels() []listedModel {
	px, ok := loadProxy()
	if !ok {
		return nil
	}
	return append(listed(providerChat, px.chatModels()), listed(providerResponses, px.responsesModels())...)
}

type ocServer struct {
	cmd    *exec.Cmd
	base   string
	pass   string
	http   *http.Client
	stderr tailBuffer
	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
}

// opencodeConfig adds the fleet's proxy as two providers. opencode merges it
// over the user's own config, which is never written.
func opencodeConfig() string {
	px, ok := loadProxy()
	if !ok {
		return ""
	}
	provider := func(npm string, ids []string) map[string]any {
		models := map[string]any{}
		for _, id := range ids {
			models[id] = map[string]string{"name": id}
		}
		return map[string]any{"npm": npm, "options": map[string]string{"baseURL": px.Base, "apiKey": placeholderKey}, "models": models}
	}
	providers := map[string]any{}
	if ids := px.chatModels(); len(ids) > 0 {
		providers[providerChat] = provider("@ai-sdk/openai-compatible", ids)
	}
	if ids := px.responsesModels(); len(ids) > 0 {
		providers[providerResponses] = provider("@ai-sdk/openai", ids)
	}
	if len(providers) == 0 {
		return ""
	}
	b, _ := json.Marshal(map[string]any{"provider": providers})
	return string(b)
}

// server returns the running server, starting one if needed.
func (h *opencodeHarness) server(d *daemon) (*ocServer, error) {
	h.startMu.Lock()
	defer h.startMu.Unlock()
	h.mu.Lock()
	srv := h.srv
	h.used = time.Now()
	h.mu.Unlock()
	if srv != nil {
		return srv, nil
	}

	var pw [16]byte
	_, _ = rand.Read(pw[:])
	ctx, cancel := context.WithCancel(context.Background())
	srv = &ocServer{pass: hex.EncodeToString(pw[:]), http: &http.Client{}, done: make(chan struct{}), ctx: ctx, cancel: cancel}
	// Only this daemon may drive it: it listens on loopback behind a password.
	cmd := exec.Command("opencode", "serve", "--hostname", "127.0.0.1", "--port", "0")
	cmd.Dir = d.home
	cmd.Env = append(os.Environ(), "OPENCODE_SERVER_PASSWORD="+srv.pass)
	if cfg := opencodeConfig(); cfg != "" {
		cmd.Env = append(cmd.Env, "OPENCODE_CONFIG_CONTENT="+cfg)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = &srv.stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	srv.cmd = cmd

	found := make(chan string, 1)
	go func() {
		readLines(stdout, func(line []byte) {
			if u := opencodeListening.Find(line); u != nil {
				select {
				case found <- strings.TrimRight(string(u), "/"):
				default:
				}
			}
		})
	}()
	go func() {
		err := cmd.Wait()
		cancel()
		close(srv.done)
		h.lost(d, srv, err)
	}()

	select {
	case srv.base = <-found:
	case <-srv.done:
		return nil, fmt.Errorf("opencode serve exited: %s", srv.stderr.String())
	case <-time.After(opencodeStartWait):
		srv.stop()
		return nil, errors.New("opencode serve did not report its address")
	}
	h.mu.Lock()
	h.srv, h.streams = srv, map[string]bool{}
	h.mu.Unlock()
	return srv, nil
}

// lost ends every session on a server that has gone.
func (h *opencodeHarness) lost(d *daemon, srv *ocServer, err error) {
	h.mu.Lock()
	if h.srv != srv {
		h.mu.Unlock()
		return
	}
	h.srv = nil
	procs := h.procs
	h.procs = map[string]*ocProc{}
	h.mu.Unlock()
	if err != nil {
		if tail := srv.stderr.String(); tail != "" {
			err = fmt.Errorf("%w: %s", err, tail)
		}
	}
	for _, p := range procs {
		d.onExit(p, err)
	}
}

func (s *ocServer) stop() {
	s.cancel()
	_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
	}
}

// call sends one request to the server for dir; out may be nil, or a
// *json.RawMessage to keep the body as is.
func (s *ocServer) call(method, path, dir string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	u := s.base + path
	if dir != "" {
		u += "?" + url.Values{"directory": {dir}}.Encode()
	}
	ctx, cancel := context.WithTimeout(s.ctx, opencodeCallWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.SetBasicAuth("opencode", s.pass)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxFrame))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("opencode %s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(raw))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// watch follows one directory's event stream for as long as the server runs.
func (h *opencodeHarness) watch(d *daemon, srv *ocServer, dir string) {
	h.mu.Lock()
	if h.srv != srv || h.streams[dir] {
		h.mu.Unlock()
		return
	}
	h.streams[dir] = true
	h.mu.Unlock()
	go func() {
		for srv.ctx.Err() == nil {
			if err := h.follow(d, srv, dir); err != nil && srv.ctx.Err() == nil {
				log.Printf("agent: opencode events for %s: %v", dir, err)
			}
			time.Sleep(time.Second)
		}
	}()
}

func (h *opencodeHarness) follow(d *daemon, srv *ocServer, dir string) error {
	req, err := http.NewRequestWithContext(srv.ctx, http.MethodGet,
		srv.base+"/event?"+url.Values{"directory": {dir}}.Encode(), nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth("opencode", srv.pass)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := srv.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	readLines(resp.Body, func(line []byte) {
		if data, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			h.dispatch(d, srv, bytes.TrimSpace(data))
		}
	})
	return nil
}

type ocEvent struct {
	Type       string `json:"type"`
	Properties struct {
		ID        string `json:"id"`
		SessionID string `json:"sessionID"`
		Info      struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
		} `json:"info"`
		Part struct {
			SessionID string `json:"sessionID"`
			Type      string `json:"type"`
			State     struct {
				Status string `json:"status"`
			} `json:"state"`
		} `json:"part"`
		Status struct {
			Type string `json:"type"`
		} `json:"status"`
	} `json:"properties"`
}

func (h *opencodeHarness) dispatch(d *daemon, srv *ocServer, raw []byte) {
	var ev ocEvent
	if json.Unmarshal(raw, &ev) != nil {
		return
	}
	pr := ev.Properties
	sid := cmpOr(pr.SessionID, pr.Info.SessionID, pr.Part.SessionID)
	if sid == "" && strings.HasPrefix(ev.Type, "session.") {
		sid = pr.Info.ID
	}
	h.mu.Lock()
	p := h.procs[sid]
	h.mu.Unlock()
	if p == nil {
		return
	}

	sig := sigNone
	switch {
	case ev.Type == "session.status" && pr.Status.Type == "idle", ev.Type == "session.idle":
		if p.isStreaming() {
			sig = sigSettled
		}
		p.setBusy(false)
	case ev.Type == "session.status":
		if !p.isStreaming() {
			sig = sigBusy
		}
		p.setBusy(true)
	case ev.Type == "session.updated":
		sig = sigRenamed
	case ev.Type == "message.part.updated" && pr.Part.Type == "tool" && pr.Part.State.Status == "completed":
		sig = sigTouched
	case ev.Type == "permission.updated" || ev.Type == "permission.asked":
		go p.allow(srv, pr.ID)
	}
	p.touch()
	d.onEvent(p, json.RawMessage(raw), sig)
}

func cmpOr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

type ocSession struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Directory string `json:"directory"`
	ParentID  string `json:"parentID"`
	Time      struct {
		Created int64 `json:"created"`
		Updated int64 `json:"updated"`
	} `json:"time"`
}

// list asks the server, which opencode may keep in files or a database. With
// no server running the last answer stands, and the first listing starts one.
func (h *opencodeHarness) list(d *daemon) []sessionSummary {
	h.mu.Lock()
	srv, cached, kicked := h.srv, h.cached, h.kicked
	h.kicked = true
	h.mu.Unlock()
	if srv == nil {
		if !kicked {
			go func() {
				if _, err := h.server(d); err != nil {
					log.Printf("agent: opencode: %v", err)
					return
				}
				d.broadcastSessions()
			}()
		}
		return cached
	}

	var projects []struct {
		Worktree string `json:"worktree"`
	}
	if err := srv.call(http.MethodGet, "/project", "", nil, &projects); err != nil {
		log.Printf("agent: opencode projects: %v", err)
		return cached
	}
	var out []sessionSummary
	for _, pr := range projects {
		var sessions []ocSession
		if err := srv.call(http.MethodGet, "/session", pr.Worktree, nil, &sessions); err != nil {
			continue
		}
		for _, s := range sessions {
			if s.ParentID != "" {
				continue
			}
			out = append(out, sessionSummary{Key: sessionKey("opencode", s.ID), Harness: "opencode", ID: s.ID,
				Cwd: s.Directory, Title: clip(s.Title), Updated: s.Time.Updated,
				Created: time.UnixMilli(s.Time.Created).UTC().Format(time.RFC3339)})
		}
	}
	h.mu.Lock()
	h.cached = out
	h.mu.Unlock()
	return out
}

func (h *opencodeHarness) start(d *daemon, id, cwd string, model *modelRef) (proc, error) {
	srv, err := h.server(d)
	if err != nil {
		return nil, err
	}
	if id != "" {
		cwd = ""
		for _, s := range h.list(d) {
			if s.ID == id {
				cwd = s.Cwd
			}
		}
		if cwd == "" {
			return nil, errors.New("no opencode session " + id + " in this vm")
		}
	} else {
		var s ocSession
		if err := srv.call(http.MethodPost, "/session", cwd, map[string]any{}, &s); err != nil {
			return nil, err
		}
		if s.ID == "" {
			return nil, errors.New("opencode did not report a session id")
		}
		id = s.ID
	}

	p := &ocProc{base: newBase("opencode", cwd), d: d, h: h, srv: srv}
	p.sessionID = id
	close(p.ready)
	if model != nil {
		p.current = model
	} else {
		p.current = p.lastModel()
	}
	h.mu.Lock()
	if other := h.procs[id]; other != nil {
		h.mu.Unlock()
		return other, nil
	}
	h.procs[id] = p
	h.mu.Unlock()
	h.watch(d, srv, cwd)
	return p, nil
}

// reap stops the server once no session on it has been used for a while.
func (h *opencodeHarness) reap(*daemon) {
	h.mu.Lock()
	srv := h.srv
	idle := len(h.procs) == 0 && time.Since(h.used) > procIdle
	h.mu.Unlock()
	if srv != nil && idle {
		srv.stop()
	}
}

func (h *opencodeHarness) shutdown() {
	h.mu.Lock()
	srv := h.srv
	h.mu.Unlock()
	if srv != nil {
		srv.stop()
	}
}

// ocProc is one opencode session on the shared server; opencode takes the
// model with each prompt, so the one picked here is kept here.
type ocProc struct {
	*base
	d   *daemon
	h   *opencodeHarness
	srv *ocServer

	current *modelRef
}

func (p *ocProc) history() (json.RawMessage, error) {
	var raw json.RawMessage
	if err := p.srv.call(http.MethodGet, "/session/"+url.PathEscape(p.sessionID)+"/message", p.dir, nil, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return json.RawMessage("[]"), nil
	}
	return raw, nil
}

// lastModel is what the session last ran on.
func (p *ocProc) lastModel() *modelRef {
	var msgs []struct {
		Info struct {
			ProviderID string `json:"providerID"`
			ModelID    string `json:"modelID"`
		} `json:"info"`
	}
	raw, err := p.history()
	if err != nil || json.Unmarshal(raw, &msgs) != nil {
		return nil
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if m := msgs[i].Info; m.ModelID != "" {
			return &modelRef{Provider: m.ProviderID, ID: m.ModelID}
		}
	}
	return nil
}

func (p *ocProc) model() *modelRef {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.current
}

func (p *ocProc) prompt(text string, images []image) error {
	parts := []map[string]any{{"type": "text", "text": text}}
	for _, i := range images {
		parts = append(parts, map[string]any{"type": "file", "mime": i.Mime, "filename": "image",
			"url": "data:" + i.Mime + ";base64," + i.Data})
	}
	body := map[string]any{"parts": parts}
	if m := p.model(); m != nil {
		body["model"] = map[string]string{"providerID": m.Provider, "modelID": m.ID}
	}
	p.touch()
	return p.srv.call(http.MethodPost, "/session/"+url.PathEscape(p.sessionID)+"/prompt_async", p.dir, body, nil)
}

func (p *ocProc) abort() error {
	return p.srv.call(http.MethodPost, "/session/"+url.PathEscape(p.sessionID)+"/abort", p.dir, nil, nil)
}

// allow answers opencode's permission questions; its newer servers moved the
// endpoint, so both are tried.
func (p *ocProc) allow(srv *ocServer, id string) {
	if id == "" {
		return
	}
	err := srv.call(http.MethodPost, "/session/"+url.PathEscape(p.sessionID)+"/permissions/"+url.PathEscape(id), p.dir,
		map[string]string{"response": "always"}, nil)
	if err != nil {
		err = srv.call(http.MethodPost, "/permission/"+url.PathEscape(id)+"/reply", p.dir, map[string]string{"reply": "always"}, nil)
	}
	if err != nil {
		log.Printf("agent: opencode permission %s: %v", id, err)
	}
}

func (p *ocProc) models() ([]listedModel, error) {
	var res struct {
		Providers []struct {
			ID     string `json:"id"`
			Models map[string]struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := p.srv.call(http.MethodGet, "/config/providers", p.dir, nil, &res); err != nil {
		return nil, err
	}
	var out []listedModel
	for _, pr := range res.Providers {
		for key, m := range pr.Models {
			out = append(out, listedModel{Provider: pr.ID, ID: cmpOr(m.ID, key), Name: m.Name})
		}
	}
	sortModels(out)
	return out, nil
}

func (p *ocProc) rename(name string) error {
	err := p.srv.call(http.MethodPatch, "/session/"+url.PathEscape(p.sessionID), p.dir, map[string]string{"title": name}, nil)
	if err != nil {
		return err
	}
	p.setName(name)
	return nil
}

func (p *ocProc) setModel(m modelRef) error {
	p.mu.Lock()
	p.current = &m
	p.mu.Unlock()
	return nil
}

// stop only lets go of the session; the server keeps running the turn.
func (p *ocProc) stop() {
	p.h.mu.Lock()
	if p.h.procs[p.sessionID] == p {
		delete(p.h.procs, p.sessionID)
	}
	p.h.mu.Unlock()
	p.d.onExit(p, nil)
}
