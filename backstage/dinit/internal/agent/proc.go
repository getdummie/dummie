package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// proc is one running agent session, whatever cli drives it.
type proc interface {
	key() string
	id() string
	harness() string
	cwd() string
	// history is the session so far in the harness's own shape; the browser
	// turns it into a chat.
	history() (json.RawMessage, error)
	model() *modelRef
	prompt(text string, images []image) error
	abort() error
	models() ([]listedModel, error)
	setModel(m modelRef) error
	rename(name string) error
	// name is the one set through rename, which outlives the harness's store.
	name() string
	isStreaming() bool
	lastUsed() time.Time
	stop()
}

// signal tells the daemon what an event means for the session list and the
// diff pane; the event itself goes to the browser untouched.
type signal int

const (
	sigNone signal = iota
	sigBusy
	sigSettled
	sigTouched
	sigRenamed
)

type modelRef struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

type image struct {
	Data string
	Mime string
}

// base is what every proc keeps the same way.
type base struct {
	harnessName string
	sessionID   string
	dir         string

	mu    sync.Mutex
	busy  bool
	used  time.Time
	named string
	// ready closes once start has returned, so no event or exit is reported
	// for a session the daemon does not know yet.
	ready chan struct{}
}

func newBase(harness, cwd string) *base {
	return &base{harnessName: harness, dir: cwd, used: time.Now(), ready: make(chan struct{})}
}

func (b *base) key() string     { return sessionKey(b.harnessName, b.sessionID) }
func (b *base) id() string      { return b.sessionID }
func (b *base) harness() string { return b.harnessName }
func (b *base) cwd() string     { return b.dir }

func (b *base) name() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.named
}

func (b *base) setName(n string) {
	b.mu.Lock()
	b.named = n
	b.mu.Unlock()
}

var errNoRename = errors.New("this harness cannot rename a session")

func (b *base) isStreaming() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.busy
}

func (b *base) lastUsed() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

func (b *base) touch() {
	b.mu.Lock()
	b.used = time.Now()
	b.mu.Unlock()
}

func (b *base) setBusy(v bool) {
	b.mu.Lock()
	b.busy, b.used = v, time.Now()
	b.mu.Unlock()
}

func (b *base) isReady() bool {
	select {
	case <-b.ready:
		return true
	default:
		return false
	}
}

func sessionKey(harness, id string) string { return harness + ":" + id }

// stdio is a child speaking one json object per line on stdin and stdout.
type stdio struct {
	cmd     *exec.Cmd
	writeMu sync.Mutex
	stdin   io.WriteCloser
	stderr  tailBuffer
	done    chan struct{}

	mu      sync.Mutex
	seq     int
	pending map[string]chan json.RawMessage
}

func newStdio(cmd *exec.Cmd) *stdio {
	return &stdio{cmd: cmd, done: make(chan struct{}), pending: map[string]chan json.RawMessage{}}
}

// start runs cmd in its own process group. onExit runs once stdout is drained
// and the process has gone.
func (s *stdio) start(onLine func([]byte), onExit func(error)) error {
	cmd := s.cmd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = &s.stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	s.stdin = stdin
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		readLines(stdout, onLine)
		err := cmd.Wait()
		s.failPending()
		close(s.done)
		if tail := s.stderr.String(); err != nil && tail != "" {
			err = fmt.Errorf("%w: %s", err, tail)
		}
		onExit(err)
	}()
	return nil
}

// readLines splits only on LF: json from node can carry U+2028 inside strings.
func readLines(r io.Reader, onLine func([]byte)) {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		line = bytes.TrimRight(line, "\r\n")
		if len(line) > 0 {
			onLine(line)
		}
		if err != nil {
			return
		}
	}
}

func (s *stdio) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.stdin.Write(append(b, '\n'))
	return err
}

func (s *stdio) nextID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return s.seq
}

// await registers id, runs write and waits for resolve(id).
func (s *stdio) await(id string, timeout time.Duration, write func() error) (json.RawMessage, error) {
	ch := make(chan json.RawMessage, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	if err := write(); err != nil {
		s.drop(id)
		return nil, err
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case res, ok := <-ch:
		if !ok {
			return nil, errors.New("the agent exited")
		}
		return res, nil
	case <-t.C:
		s.drop(id)
		return nil, errors.New("the agent did not answer in time")
	}
}

func (s *stdio) resolve(id string, raw json.RawMessage) bool {
	s.mu.Lock()
	ch, ok := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if ok {
		ch <- raw
	}
	return ok
}

func (s *stdio) drop(id string) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

func (s *stdio) failPending() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.pending {
		close(ch)
		delete(s.pending, id)
	}
}

// stop closes stdin, which every harness here treats as an orderly shutdown,
// and kills the process group if it has not gone in a few seconds.
func (s *stdio) stop() {
	_ = s.stdin.Close()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
	}
}

// jsonrpc is json-rpc 2.0 over stdio, as codex's app-server and acp speak it.
type jsonrpc struct {
	*stdio
	// tagged adds "jsonrpc":"2.0", which codex leaves out and acp requires.
	tagged    bool
	onNotify  func(method string, line []byte)
	onRequest func(method string, params json.RawMessage) (any, error)
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func (j *jsonrpc) handle(line []byte) {
	var m rpcMessage
	if json.Unmarshal(line, &m) != nil {
		return
	}
	switch {
	case m.Method != "" && len(m.ID) > 0:
		go j.answer(m)
	case m.Method != "":
		j.onNotify(m.Method, line)
	case len(m.ID) > 0:
		j.resolve(string(m.ID), json.RawMessage(line))
	}
}

func (j *jsonrpc) answer(m rpcMessage) {
	out := map[string]any{"id": m.ID}
	if j.tagged {
		out["jsonrpc"] = "2.0"
	}
	res, err := j.onRequest(m.Method, m.Params)
	if err != nil {
		out["error"] = rpcError{Code: -32601, Message: err.Error()}
	} else {
		out["result"] = res
	}
	_ = j.send(out)
}

func (j *jsonrpc) call(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	id := j.nextID()
	msg := map[string]any{"id": id, "method": method, "params": params}
	if j.tagged {
		msg["jsonrpc"] = "2.0"
	}
	raw, err := j.await(strconv.Itoa(id), timeout, func() error { return j.send(msg) })
	if err != nil {
		return nil, err
	}
	var m rpcMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m.Error != nil {
		return nil, fmt.Errorf("%s: %s", method, m.Error.Message)
	}
	return m.Result, nil
}

func (j *jsonrpc) notify(method string, params any) error {
	msg := map[string]any{"method": method}
	if params != nil {
		msg["params"] = params
	}
	if j.tagged {
		msg["jsonrpc"] = "2.0"
	}
	return j.send(msg)
}

type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const tailMax = 4 << 10

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > tailMax {
		t.buf = t.buf[len(t.buf)-tailMax:]
	}
	return len(b), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(bytes.TrimSpace(t.buf))
}
