package dpipe

import (
	"errors"
	"log/slog"
	"net"
	"sync"

	"dpipe/internal/control"
	"dpipe/internal/xnet"
)

// Forward is a listener owned by dpipe that dials a fixed target for every
// accepted connection.
type Forward struct {
	ID     string
	Listen string
	Target string

	ln net.Listener
}

// ForwardManager owns all listen_forward jobs.
type ForwardManager struct {
	log *slog.Logger
	reg *Registry

	mu sync.Mutex
	m  map[string]*Forward
}

// NewForwardManager returns an empty manager.
func NewForwardManager(log *slog.Logger, reg *Registry) *ForwardManager {
	return &ForwardManager{log: log, reg: reg, m: map[string]*Forward{}}
}

// Count returns the number of active forwards.
func (fm *ForwardManager) Count() int {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	return len(fm.m)
}

// Start binds listen and serves it until Stop or process exit.
func (fm *ForwardManager) Start(id, listen, target string) (*Forward, error) {
	if listen == "" || target == "" {
		return nil, errors.New("listen_forward requires listen and target")
	}
	ln, err := xnet.Listen("tcp", listen, false)
	if err != nil {
		return nil, err
	}
	return fm.adopt(id, listen, target, ln), nil
}

// Adopt takes over a listener received during a handover.
func (fm *ForwardManager) Adopt(id, listen, target string, ln net.Listener) *Forward {
	return fm.adopt(id, listen, target, ln)
}

func (fm *ForwardManager) adopt(id, listen, target string, ln net.Listener) *Forward {
	f := &Forward{ID: id, Listen: listen, Target: target, ln: ln}
	fm.mu.Lock()
	fm.m[id] = f
	fm.mu.Unlock()

	log := fm.log.With("forward", id, "listen", listen, "target", target)
	log.Info("listen_forward serving")
	go fm.serve(f, log)
	return f
}

func (fm *ForwardManager) serve(f *Forward, log *slog.Logger) {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			log.Debug("listen_forward listener closed", "err", err)
			return
		}
		go fm.handle(f, c, log)
	}
}

func (fm *ForwardManager) handle(f *Forward, client net.Conn, log *slog.Logger) {
	id := control.NewID()
	backend, err := net.DialTimeout("tcp", f.Target, defaultDialTimeout)
	if err != nil {
		log.Warn("listen_forward dial failed", "id", id, "err", err)
		_ = client.Close()
		return
	}
	fm.reg.Add(id)
	defer fm.reg.Done(id)
	log.Debug("listen_forward conn start", "id", id)
	Pipe(client, backend)
	log.Debug("listen_forward conn end", "id", id)
}

// Stop closes a forward's listener. Connections already in flight keep running.
func (fm *ForwardManager) Stop(id string) error {
	fm.mu.Lock()
	f := fm.m[id]
	delete(fm.m, id)
	fm.mu.Unlock()
	if f == nil {
		return errors.New("unknown listen_forward id")
	}
	fm.log.Info("listen_forward stopped", "forward", id, "listen", f.Listen)
	return f.ln.Close()
}

// Descriptors returns one SockDesc plus one listener fd per forward, in matching
// order, for the handover handshake.
func (fm *ForwardManager) Descriptors() ([]control.SockDesc, []int, error) {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	var (
		descs []control.SockDesc
		fds   []int
	)
	for id, f := range fm.m {
		fd, err := xnet.ListenerFD(f.ln)
		if err != nil {
			return nil, nil, err
		}
		descs = append(descs, control.SockDesc{
			Kind:   control.SockListenForward,
			ID:     id,
			Listen: f.Listen,
			Target: f.Target,
		})
		fds = append(fds, fd)
	}
	return descs, fds, nil
}

// CloseListeners closes every forward listener without dropping live conns.
func (fm *ForwardManager) CloseListeners() {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	for _, f := range fm.m {
		_ = f.ln.Close()
	}
}
