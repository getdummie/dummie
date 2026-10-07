package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// client is one attached browser tab.
type client struct {
	d    *daemon
	conn net.Conn
	out  chan []byte

	closeOnce sync.Once
	gone      chan struct{}

	mu      sync.Mutex
	watch   string
	uploads map[string]*upload
	writes  map[string]*bytes.Buffer
}

type request struct {
	T    string `json:"t"`
	Req  string `json:"req"`
	Key  string `json:"key"`
	ID   string `json:"id"`
	Cwd  string `json:"cwd"`

	Harness string    `json:"harness"`
	Model   *modelRef `json:"model"`

	Text        string       `json:"text"`
	Attachments []attachment `json:"attachments"`

	Name string `json:"name"`
	Data string `json:"data"`
	Last bool   `json:"last"`

	Path  string `json:"path"`
	Hash  string `json:"hash"`
	Force bool   `json:"force"`
}

type attachment struct {
	Path string `json:"path"`
	Mime string `json:"mime"`
}

func (d *daemon) serveClient(conn net.Conn) {
	c := &client{d: d, conn: conn, out: make(chan []byte, clientQueue), gone: make(chan struct{}),
		uploads: map[string]*upload{}, writes: map[string]*bytes.Buffer{}}
	d.mu.Lock()
	d.clients[c] = struct{}{}
	d.mu.Unlock()

	go c.writeLoop()
	defer func() {
		d.mu.Lock()
		delete(d.clients, c)
		d.mu.Unlock()
		c.setWatch("")
		c.abortUploads()
		c.close()
	}()

	for {
		op, payload, err := ReadFrame(conn)
		if err != nil {
			return
		}
		if op != OpText {
			continue
		}
		var r request
		if err := json.Unmarshal(payload, &r); err != nil {
			c.send(map[string]any{"t": "error", "code": "bad_request", "message": "not json"})
			continue
		}
		c.dispatch(r)
	}
}

func (c *client) close() {
	c.closeOnce.Do(func() {
		close(c.gone)
		_ = c.conn.Close()
	})
}

func (c *client) writeLoop() {
	for {
		select {
		case b := <-c.out:
			if err := WriteFrame(c.conn, OpText, b); err != nil {
				c.close()
				return
			}
		case <-c.gone:
			return
		}
	}
}

// sendRaw never blocks: a tab that cannot keep up is dropped and reconnects,
// rather than stalling every other tab and the agents behind them.
func (c *client) sendRaw(b []byte) {
	select {
	case c.out <- b:
	case <-c.gone:
	default:
		log.Printf("agent: dropping a client that fell %d messages behind", clientQueue)
		c.close()
	}
}

func (c *client) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.sendRaw(b)
}

func (c *client) fail(r request, err error) {
	c.send(map[string]any{"t": "error", "req": r.Req, "key": r.Key, "message": err.Error()})
}

func (c *client) dispatch(r request) {
	switch r.T {
	case "hello":
		c.send(c.d.hello())
	case "sessions":
		c.send(map[string]any{"t": "sessions", "req": r.Req, "sessions": c.d.sessions()})
	case "open":
		go c.open(r)
	case "prompt":
		go c.prompt(r)
	case "abort":
		go c.withProc(r, func(p proc) error { return p.abort() })
	case "set_model":
		go c.withProc(r, func(p proc) error {
			if r.Model == nil {
				return errors.New("no model given")
			}
			return p.setModel(*r.Model)
		})
	case "models":
		go c.models(r)
	case "rename":
		go c.rename(r)
	case "upload":
		c.upload(r)
	case "watch":
		c.setWatch(r.Cwd)
	case "git_init":
		go c.gitInit(r)
	case "stat":
		c.stat(r)
	case "mkdir":
		c.mkdir(r)
	case "read_file":
		go c.readFile(r)
	case "write_file":
		c.writeFile(r)
	case "retire":
		c.d.retire()
	default:
		c.fail(r, fmt.Errorf("unknown message %q", r.T))
	}
}

func (d *daemon) hello() map[string]any {
	harnesses := make([]map[string]any, 0, len(d.harnesses))
	for _, h := range d.harnesses {
		e := map[string]any{"name": h.name(), "available": true, "steers": h.steers(), "renames": h.renames(), "models": []listedModel{}}
		if err := available(h); err != nil {
			e["available"], e["error"] = false, err.Error()
		} else if m := h.presetModels(); m != nil {
			e["models"] = m
		}
		harnesses = append(harnesses, e)
	}
	return map[string]any{
		"t": "hello", "version": d.version, "home": d.home, "cwd": d.defaultCwd(),
		"harnesses": harnesses,
	}
}

// open takes a session by harness and id (what the page's url carries), or a
// harness and cwd for a new one.
func (c *client) open(r request) {
	name := r.Harness
	if name == "" {
		name = "pi"
	}
	p, err := c.d.open(name, r.ID, c.d.expandHome(r.Cwd), r.Model)
	if err != nil {
		c.fail(r, err)
		return
	}
	hist, err := p.history()
	if err != nil {
		c.fail(r, err)
		return
	}
	c.send(map[string]any{"t": "opened", "req": r.Req, "key": p.key(), "id": p.id(), "harness": p.harness(),
		"cwd": p.cwd(), "streaming": p.isStreaming(), "model": p.model(), "history": hist})
}

func (c *client) notRunning(r request) {
	c.send(map[string]any{"t": "error", "req": r.Req, "key": r.Key, "code": "not_running", "message": "this session is not running; open it again"})
}

func (c *client) withProc(r request, fn func(proc) error) {
	p, ok := c.d.proc(r.Key)
	if !ok {
		c.notRunning(r)
		return
	}
	if err := fn(p); err != nil {
		c.fail(r, err)
		return
	}
	c.send(map[string]any{"t": "ok", "req": r.Req})
}

func (c *client) models(r request) {
	p, ok := c.d.proc(r.Key)
	if !ok {
		c.notRunning(r)
		return
	}
	models, err := p.models()
	if err != nil {
		c.fail(r, err)
		return
	}
	if models == nil {
		models = []listedModel{}
	}
	c.send(map[string]any{"t": "models", "req": r.Req, "models": models, "current": p.model()})
}

func (c *client) rename(r request) {
	name := strings.TrimSpace(r.Name)
	if name == "" || len([]rune(name)) > maxTitleRunes {
		c.fail(r, fmt.Errorf("a session name must be 1 to %d characters", maxTitleRunes))
		return
	}
	if r.ID == "" {
		c.fail(r, errors.New("no session given"))
		return
	}
	// Any listed session can be renamed, so one not running is started for it.
	p, err := c.d.open(r.Harness, r.ID, "", nil)
	if err == nil {
		err = p.rename(name)
	}
	if err != nil {
		c.fail(r, err)
		return
	}
	c.d.broadcastSessions()
	c.send(map[string]any{"t": "ok", "req": r.Req})
}

// prompt passes images inline and anything else as a path the agent can read.
func (c *client) prompt(r request) {
	var images []image
	var files []string
	for _, a := range r.Attachments {
		if !c.d.ownsUpload(a.Path) {
			c.fail(r, fmt.Errorf("%s is not an upload", a.Path))
			return
		}
		if strings.HasPrefix(a.Mime, "image/") {
			b, err := os.ReadFile(a.Path)
			if err != nil {
				c.fail(r, err)
				return
			}
			images = append(images, image{Data: base64.StdEncoding.EncodeToString(b), Mime: a.Mime})
			continue
		}
		files = append(files, a.Path)
	}
	c.withProc(r, func(p proc) error { return p.prompt(withFiles(r.Text, files), images) })
}

func (c *client) gitInit(r request) {
	out, err := exec.Command("git", "-C", c.d.expandHome(r.Cwd), "init").CombinedOutput()
	if err != nil {
		c.fail(r, fmt.Errorf("git init: %s", strings.TrimSpace(string(out))))
		return
	}
	c.d.poke(r.Cwd)
	c.send(map[string]any{"t": "ok", "req": r.Req})
}

func (c *client) stat(r request) {
	path := c.d.expandHome(r.Path)
	fi, err := os.Stat(path)
	c.send(map[string]any{"t": "stat", "req": r.Req, "path": path, "exists": err == nil, "dir": err == nil && fi.IsDir()})
}

// mkdir creates a session's directory and any parents it needs.
func (c *client) mkdir(r request) {
	path := c.d.expandHome(r.Path)
	if !filepath.IsAbs(path) {
		c.fail(r, fmt.Errorf("%s is not an absolute path", r.Path))
		return
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		c.fail(r, err)
		return
	}
	c.send(map[string]any{"t": "ok", "req": r.Req, "path": path})
}

func (c *client) readFile(r request) {
	f, err := readEditable(r.Cwd, r.Path)
	if err != nil {
		c.fail(r, err)
		return
	}
	c.send(map[string]any{"t": "file", "req": r.Req, "path": r.Path, "file": f})
}

func (c *client) writeFile(r request) {
	c.mu.Lock()
	buf, ok := c.writes[r.Req]
	if !ok {
		buf = &bytes.Buffer{}
		c.writes[r.Req] = buf
	}
	buf.WriteString(r.Data)
	size := buf.Len()
	if r.Last || size > maxEditable {
		delete(c.writes, r.Req)
	}
	c.mu.Unlock()

	if size > maxEditable {
		c.fail(r, fmt.Errorf("files over %d KiB cannot be edited here", maxEditable>>10))
		return
	}
	if !r.Last {
		c.send(map[string]any{"t": "write_ack", "req": r.Req})
		return
	}
	hash, err := writeEditable(r.Cwd, r.Path, r.Hash, r.Force, buf.Bytes())
	if errors.Is(err, errConflict) {
		c.send(map[string]any{"t": "error", "req": r.Req, "code": "conflict", "message": err.Error()})
		return
	}
	if err != nil {
		c.fail(r, err)
		return
	}
	c.d.poke(r.Cwd)
	c.send(map[string]any{"t": "written", "req": r.Req, "path": r.Path, "hash": hash})
}

func (c *client) setWatch(cwd string) {
	c.mu.Lock()
	prev := c.watch
	c.watch = cwd
	c.mu.Unlock()
	if prev == cwd {
		return
	}
	if prev != "" {
		c.d.unwatch(prev, c)
	}
	if cwd != "" {
		c.d.watchFor(cwd, c)
	}
}
