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

const piBinary = "pi"

func piAvailable() error {
	_, err := exec.LookPath(piBinary)
	return err
}

// piProc is one `pi --mode rpc` child running one session. Its key is the
// session file pi reports, which is also how the session is listed.
type piProc struct {
	key string
	id  string
	cwd string
	cmd *exec.Cmd

	writeMu sync.Mutex
	stdin   io.WriteCloser

	mu        sync.Mutex
	pending   map[string]chan json.RawMessage
	seq       int
	streaming bool
	used      time.Time
	stderr    tailBuffer
	done      chan struct{}
}

type piRecord struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type piState struct {
	SessionFile string `json:"sessionFile"`
	SessionID   string `json:"sessionId"`
}

type piResponse struct {
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func startPi(cwd, file string, onEvent func(*piProc, json.RawMessage, string), onExit func(*piProc, error)) (*piProc, error) {
	if err := piAvailable(); err != nil {
		return nil, errors.New("pi is not installed in this vm")
	}
	args := []string{"--mode", "rpc"}
	if file != "" {
		args = append(args, "--session", file)
	}
	cmd := exec.Command(piBinary, args...)
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	p := &piProc{cwd: cwd, cmd: cmd, pending: map[string]chan json.RawMessage{}, used: time.Now(), done: make(chan struct{})}
	cmd.Stderr = &p.stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	p.stdin = stdin
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	ready := make(chan struct{})
	go p.read(stdout, ready, onEvent)
	go func() {
		err := cmd.Wait()
		p.failPending()
		close(p.done)
		if tail := p.stderr.String(); err != nil && tail != "" {
			err = fmt.Errorf("%w: %s", err, tail)
		}
		<-ready
		onExit(p, err)
	}()

	res, err := p.call(json.RawMessage(`{"type":"get_state"}`), 30*time.Second)
	if err == nil {
		var st piState
		if err = json.Unmarshal(res, &st); err == nil && st.SessionFile == "" {
			err = errors.New("pi did not report a session file")
		}
		p.key, p.id = st.SessionFile, st.SessionID
	}
	close(ready)
	if err != nil {
		p.stop()
		return nil, fmt.Errorf("pi did not start: %w", err)
	}
	return p, nil
}

// read splits only on LF: pi's json can carry U+2028 inside strings.
func (p *piProc) read(r io.Reader, ready <-chan struct{}, onEvent func(*piProc, json.RawMessage, string)) {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		line = bytes.TrimRight(line, "\r\n")
		if len(line) > 0 {
			p.handle(line, ready, onEvent)
		}
		if err != nil {
			return
		}
	}
}

func (p *piProc) handle(line []byte, ready <-chan struct{}, onEvent func(*piProc, json.RawMessage, string)) {
	var rec piRecord
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	if rec.Type == "response" && rec.ID != "" {
		p.mu.Lock()
		ch, ok := p.pending[rec.ID]
		delete(p.pending, rec.ID)
		p.mu.Unlock()
		if ok {
			ch <- json.RawMessage(line)
		}
		return
	}
	p.mu.Lock()
	p.used = time.Now()
	switch rec.Type {
	case "agent_start":
		p.streaming = true
	case "agent_settled":
		p.streaming = false
	}
	p.mu.Unlock()
	// Nothing can be routed before the session key is known.
	select {
	case <-ready:
		onEvent(p, json.RawMessage(line), rec.Type)
	default:
	}
}

// call sends one command and returns pi's data for it, or its error.
func (p *piProc) call(cmd json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	raw, err := p.request(cmd, timeout)
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

// request sends one command and returns pi's whole response record.
func (p *piProc) request(cmd json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	var m map[string]any
	if err := json.Unmarshal(cmd, &m); err != nil {
		return nil, fmt.Errorf("command is not a json object: %w", err)
	}
	ch := make(chan json.RawMessage, 1)
	p.mu.Lock()
	p.seq++
	id := "d" + strconv.Itoa(p.seq)
	p.pending[id] = ch
	p.used = time.Now()
	p.mu.Unlock()
	m["id"] = id
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}

	p.writeMu.Lock()
	_, err = p.stdin.Write(append(b, '\n'))
	p.writeMu.Unlock()
	if err != nil {
		p.drop(id)
		return nil, err
	}

	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case res, ok := <-ch:
		if !ok {
			return nil, errors.New("pi exited")
		}
		return res, nil
	case <-t.C:
		p.drop(id)
		return nil, errors.New("pi did not answer in time")
	}
}

func (p *piProc) drop(id string) {
	p.mu.Lock()
	delete(p.pending, id)
	p.mu.Unlock()
}

func (p *piProc) failPending() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, ch := range p.pending {
		close(ch)
		delete(p.pending, id)
	}
}

func (p *piProc) isStreaming() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.streaming
}

func (p *piProc) lastUsed() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.used
}

// stop closes stdin, which pi treats as an orderly shutdown, and kills the
// process group if it has not gone in a few seconds.
func (p *piProc) stop() {
	_ = p.stdin.Close()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	}
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
