package dpipe

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"dpipe/internal/control"
	"dpipe/internal/xnet"
)

const (
	defaultDialTimeout    = 5 * time.Second
	defaultResolveTimeout = 3 * time.Second
	defaultSniffTimeout   = 5 * time.Second
)

type Server struct {
	cfg *Config
	log *slog.Logger
	mat *material

	reg *Registry
	fwd *ForwardManager

	ctrlLn *net.UnixListener
	upLn   *net.UnixListener

	mu    sync.Mutex
	peers map[*control.Peer]struct{}
	consoles map[string]int

	stopOnce sync.Once
	stopped  chan struct{}

	exitOnce sync.Once
	exitCh   chan struct{}
}

func New(cfg *Config, log *slog.Logger) (*Server, error) {
	mat, err := loadMaterial(cfg, log)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:     cfg,
		log:     log,
		mat:     mat,
		peers:    map[*control.Peer]struct{}{},
		consoles: map[string]int{},
		stopped:  make(chan struct{}),
		exitCh:   make(chan struct{}),
	}
	s.fwd = NewForwardManager(log, nil)
	s.reg = NewRegistry(s.fwd.Count)
	s.fwd.reg = s.reg
	return s, nil
}

func (s *Server) Registry() *Registry { return s.reg }

func (s *Server) Bind() error {
	ctrl, err := xnet.ListenUnix(s.cfg.ControlSocket)
	if err != nil {
		return err
	}
	up, err := xnet.ListenUnix(s.cfg.UpgradeSocket)
	if err != nil {
		_ = ctrl.Close()
		return err
	}
	s.ctrlLn, s.upLn = ctrl, up
	s.log.Info("listening", "control", s.cfg.ControlSocket, "upgrade", s.cfg.UpgradeSocket)
	return nil
}

func (s *Server) Start() {
	go s.acceptControl()
	go s.acceptUpgrade()
}

func (s *Server) Exit() <-chan struct{} { return s.exitCh }

func (s *Server) Draining() bool { return s.reg.Draining() }

func (s *Server) acceptControl() {
	for {
		c, err := s.ctrlLn.AcceptUnix()
		if err != nil {
			select {
			case <-s.stopped:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			s.log.Warn("control accept failed", "err", err)
			return
		}
		go s.serveControl(c)
	}
}

func (s *Server) serveControl(c *net.UnixConn) {
	p := control.NewPeer(control.NewConn(c), s.handle)

	s.mu.Lock()
	s.peers[p] = struct{}{}
	s.mu.Unlock()

	s.log.Debug("control connection accepted")
	err := p.Serve()

	s.mu.Lock()
	delete(s.peers, p)
	s.mu.Unlock()
	s.log.Debug("control connection closed", "err", err)
}

func (s *Server) handle(p *control.Peer, m control.Msg, fds []int) {
	switch m.Type {
	case control.TypeCopy:
		s.handleCopy(p, m, fds)
	case control.TypeSSHAccept:
		s.handleSSHAccept(p, m, fds)
	case control.TypeTLSAccept:
		s.handleTLSAccept(p, m, fds)
	case control.TypeConsoleAccept:
		s.handleConsoleAccept(p, m, fds)
	case control.TypeListenForward:
		control.CloseFDs(fds)
		s.handleListenForward(p, m)
	case control.TypeStop:
		control.CloseFDs(fds)
		if err := s.fwd.Stop(m.ID); err != nil {
			_ = p.Send(control.Err(m.ID, err.Error()), nil)
			return
		}
		_ = p.Send(control.OK(m.ID), nil)
	case control.TypeStatus:
		control.CloseFDs(fds)
		active, forwards, draining := s.reg.Snapshot()
		_ = p.Send(control.Msg{
			V: control.Version, Type: control.TypeOK, ID: m.ID,
			ActiveConns: active, ListenForwards: forwards, Draining: draining,
		}, nil)
	default:
		control.CloseFDs(fds)
		_ = p.Send(control.Err(m.ID, "unknown request type "+m.Type), nil)
	}
}

func (s *Server) handleCopy(p *control.Peer, m control.Msg, fds []int) {
	if len(fds) != 2 {
		control.CloseFDs(fds)
		_ = p.Send(control.Err(m.ID, "copy requires exactly 2 file descriptors"), nil)
		return
	}
	if s.reg.Draining() {
		control.CloseFDs(fds)
		_ = p.Send(control.Err(m.ID, "draining"), nil)
		return
	}
	client, err := xnet.FileConn(fds[0])
	if err != nil {
		control.CloseFDs(fds[1:])
		_ = p.Send(control.Err(m.ID, err.Error()), nil)
		return
	}
	backend, err := xnet.FileConn(fds[1])
	if err != nil {
		_ = client.Close()
		_ = p.Send(control.Err(m.ID, err.Error()), nil)
		return
	}

	id := msgID(m)
	log := s.log.With("id", id, "protocol", m.Protocol)
	s.reg.Add(id)
	_ = p.Send(control.OK(m.ID), nil)
	log.Info("copy start", "client", addrString(client.RemoteAddr()), "backend", addrString(backend.RemoteAddr()))

	go func() {
		defer s.reg.Done(id)
		Pipe(client, backend)
		log.Info("copy end")
	}()
}

func (s *Server) handleListenForward(p *control.Peer, m control.Msg) {
	id := msgID(m)
	if _, err := s.fwd.Start(id, m.Listen, m.Target); err != nil {
		_ = p.Send(control.Err(m.ID, err.Error()), nil)
		return
	}
	_ = p.Send(control.OK(id), nil)
}

func (s *Server) handleSSHAccept(p *control.Peer, m control.Msg, fds []int) {
	if len(fds) != 1 {
		control.CloseFDs(fds)
		_ = p.Send(control.Err(m.ID, "ssh_accept requires exactly 1 file descriptor"), nil)
		return
	}
	if !s.cfg.SSH.Enabled {
		control.CloseFDs(fds)
		_ = p.Send(control.Err(m.ID, "ssh not enabled"), nil)
		return
	}
	if s.reg.Draining() {
		control.CloseFDs(fds)
		_ = p.Send(control.Err(m.ID, "draining"), nil)
		return
	}
	client, err := xnet.FileConn(fds[0])
	if err != nil {
		_ = p.Send(control.Err(m.ID, err.Error()), nil)
		return
	}
	_ = p.Send(control.OK(m.ID), nil)
	go s.serveSSH(p, msgID(m), client)
}

func (s *Server) handleTLSAccept(p *control.Peer, m control.Msg, fds []int) {
	if len(fds) != 1 {
		control.CloseFDs(fds)
		_ = p.Send(control.Err(m.ID, "tls_accept requires exactly 1 file descriptor"), nil)
		return
	}
	if !s.cfg.TLS.Enabled {
		control.CloseFDs(fds)
		_ = p.Send(control.Err(m.ID, "tls not enabled"), nil)
		return
	}
	if s.reg.Draining() {
		control.CloseFDs(fds)
		_ = p.Send(control.Err(m.ID, "draining"), nil)
		return
	}
	client, err := xnet.FileConn(fds[0])
	if err != nil {
		_ = p.Send(control.Err(m.ID, err.Error()), nil)
		return
	}
	_ = p.Send(control.OK(m.ID), nil)
	go s.serveTLS(p, msgID(m), client)
}

func msgID(m control.Msg) string {
	if m.ID != "" {
		return m.ID
	}
	return control.NewID()
}

func (s *Server) stopAccepting() {
	s.stopOnce.Do(func() {
		close(s.stopped)
		if s.ctrlLn != nil {
			_ = s.ctrlLn.Close()
		}
		if s.upLn != nil {
			_ = s.upLn.Close()
		}
		s.fwd.CloseListeners()
	})
}

func (s *Server) closePeers() {
	s.mu.Lock()
	peers := make([]*control.Peer, 0, len(s.peers))
	for p := range s.peers {
		peers = append(peers, p)
	}
	s.mu.Unlock()
	for _, p := range peers {
		_ = p.Close()
	}
}

func (s *Server) Shutdown() {
	s.log.Info("shutting down: stop accepting, drain", "active", s.reg.Active())
	s.stopAccepting()
	s.reg.SetDraining()
	s.drain()
}

func (s *Server) drain() {
	ctx := context.Background()
	var cancel context.CancelFunc
	if d := s.cfg.DrainTimeout.D(); d > 0 {
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	if err := s.reg.WaitZero(ctx); err != nil {
		s.log.Warn("drain timed out", "active", s.reg.Active())
		return
	}
	s.log.Info("drained")
}
