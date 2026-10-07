package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	idleExit     = 30 * time.Minute
	idleCheck    = time.Minute
	procIdle     = 15 * time.Minute
	retirePoll   = 2 * time.Second
	clientQueue  = 512
	rpcTimeout   = 5 * time.Minute
	defaultWorkd = "app"
)

type daemon struct {
	version string
	home    string
	sock    string

	mu        sync.Mutex
	clients   map[*client]struct{}
	procs     map[string]proc
	harnesses []harness
	watches   map[string]*watch
	lastBusy  time.Time
	retiring  bool
}

// Serve runs the daemon until it has been idle for idleExit: no browser
// attached and no agent working.
func Serve(version string) int {
	dir, err := stateDir()
	if err != nil {
		log.Printf("agent: %v", err)
		return 1
	}
	id := binaryID(version)
	lock, err := os.OpenFile(filepath.Join(dir, "agent-"+id+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		log.Printf("agent: %v", err)
		return 1
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return 0
	}

	sock := socketPath(dir, id)
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		log.Printf("agent: listen: %v", err)
		return 1
	}
	_ = os.Chmod(sock, 0o600)
	defer os.Remove(sock)

	home, _ := os.UserHomeDir()
	// Lookups and every agent spawned from here inherit it.
	_ = os.Setenv("PATH", withUserBins(home, os.Getenv("PATH")))
	d := &daemon{
		version: version, home: home, sock: sock,
		clients: map[*client]struct{}{}, procs: map[string]proc{}, watches: map[string]*watch{},
		lastBusy: time.Now(),
		harnesses: []harness{piHarness{}, &claudeHarness{}, &codexHarness{}, newOpencodeHarness(), &geminiHarness{}},
	}
	log.Printf("agent %s serving on %s", id, sock)

	go func() {
		for range time.Tick(idleCheck) {
			d.reapProcs()
			if d.idle() {
				log.Printf("agent: idle for %s, exiting", idleExit)
				d.exit()
			}
		}
	}()

	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return 0
			}
			log.Printf("agent: accept: %v", err)
			continue
		}
		go d.serveClient(c)
	}
}

func (d *daemon) exit() {
	d.shutdown()
	_ = os.Remove(d.sock)
	os.Exit(0)
}

// retire is a newer binary asking this daemon to make way. It leaves as soon
// as no agent is mid-turn; its tabs reconnect to the new daemon.
func (d *daemon) retire() {
	d.mu.Lock()
	if d.retiring {
		d.mu.Unlock()
		return
	}
	d.retiring = true
	d.mu.Unlock()
	log.Printf("agent: a newer dinit took over; exiting once no agent is working")
	go func() {
		for ; ; time.Sleep(retirePoll) {
			if !d.anyStreaming() {
				d.exit()
			}
		}
	}()
}

func (d *daemon) anyStreaming() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range d.procs {
		if p.isStreaming() {
			return true
		}
	}
	return false
}

func (d *daemon) idle() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.clients) > 0 {
		d.lastBusy = time.Now()
		return false
	}
	for _, p := range d.procs {
		if p.isStreaming() {
			d.lastBusy = time.Now()
			return false
		}
	}
	return time.Since(d.lastBusy) > idleExit
}

// sharedServer is a harness that keeps one process for all its sessions.
type sharedServer interface {
	reap(d *daemon)
	shutdown()
}

// reapProcs stops agents nobody has used for a while; most are a whole node
// runtime, and opening the session again restarts it from its store.
func (d *daemon) reapProcs() {
	d.mu.Lock()
	var stale []proc
	for _, p := range d.procs {
		if !p.isStreaming() && time.Since(p.lastUsed()) > procIdle {
			stale = append(stale, p)
		}
	}
	d.mu.Unlock()
	for _, p := range stale {
		p.stop()
	}
	for _, h := range d.harnesses {
		if s, ok := h.(sharedServer); ok {
			s.reap(d)
		}
	}
}

func (d *daemon) shutdown() {
	d.mu.Lock()
	procs := make([]proc, 0, len(d.procs))
	for _, p := range d.procs {
		procs = append(procs, p)
	}
	d.mu.Unlock()
	for _, p := range procs {
		p.stop()
	}
	for _, h := range d.harnesses {
		if s, ok := h.(sharedServer); ok {
			s.shutdown()
		}
	}
}

func (d *daemon) defaultCwd() string {
	app := filepath.Join(d.home, defaultWorkd)
	if fi, err := os.Stat(app); err == nil && fi.IsDir() {
		return app
	}
	return d.home
}

func (d *daemon) broadcast(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for c := range d.clients {
		c.sendRaw(b)
	}
}

func (d *daemon) broadcastSessions() {
	d.broadcast(map[string]any{"t": "sessions", "sessions": d.sessions()})
}

func (d *daemon) proc(key string) (proc, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.procs[key]
	return p, ok
}

// open attaches to the process already running a session, or starts one. An
// empty id starts a new session in cwd.
func (d *daemon) open(name, id, cwd string, model *modelRef) (proc, error) {
	h, err := d.harnessFor(name)
	if err != nil {
		return nil, err
	}
	if id != "" {
		if !sessionIDPattern.MatchString(id) {
			return nil, fmt.Errorf("%q is not a session id", id)
		}
		if p, ok := d.proc(sessionKey(name, id)); ok {
			return p, nil
		}
	} else {
		if cwd == "" {
			cwd = d.defaultCwd()
		}
		if err := checkDir(cwd); err != nil {
			return nil, err
		}
	}
	if err := available(h); err != nil {
		return nil, err
	}

	p, err := h.start(d, id, cwd, model)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	if other, ok := d.procs[p.key()]; ok {
		d.mu.Unlock()
		p.stop()
		return other, nil
	}
	d.procs[p.key()] = p
	d.mu.Unlock()
	d.broadcastSessions()
	return p, nil
}

func (d *daemon) onEvent(p proc, ev json.RawMessage, sig signal) {
	d.broadcast(map[string]any{"t": "event", "key": p.key(), "ev": ev})
	switch sig {
	case sigTouched, sigSettled:
		d.poke(p.cwd())
	}
	switch sig {
	case sigBusy, sigSettled, sigRenamed:
		d.broadcastSessions()
	}
}

func (d *daemon) onExit(p proc, err error) {
	d.mu.Lock()
	if d.procs[p.key()] == p {
		delete(d.procs, p.key())
	}
	d.mu.Unlock()
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	d.broadcast(map[string]any{"t": "exit", "key": p.key(), "error": msg})
	d.broadcastSessions()
}
