package dpipe

import (
	"errors"
	"fmt"
	"net"
	"os"

	"dpipe/internal/control"
	"dpipe/internal/xnet"
)

func (s *Server) acceptUpgrade() {
	for {
		c, err := s.upLn.AcceptUnix()
		if err != nil {
			select {
			case <-s.stopped:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			s.log.Warn("upgrade accept failed", "err", err)
			return
		}
		s.handleUpgradeConn(c)
	}
}

func (s *Server) handleUpgradeConn(c *net.UnixConn) {
	defer c.Close()
	k := control.NewConn(c)

	m, fds, err := k.RecvMsg()
	control.CloseFDs(fds)
	if err != nil {
		s.log.Warn("handover: bad request", "err", err)
		return
	}
	if m.Type != control.TypeHandoverRequest {
		_ = k.SendMsg(control.Err(m.ID, "expected handover_request"), nil)
		return
	}
	s.log.Info("handover requested")

	descs, lfds, err := s.collectSockets()
	if err != nil {
		s.log.Warn("handover: could not collect listeners", "err", err)
		_ = k.SendMsg(control.Err(m.ID, err.Error()), nil)
		return
	}
	if err := k.SendMsg(control.Msg{
		V: control.Version, Type: control.TypeHandover, ID: m.ID, Sockets: descs,
	}, lfds); err != nil {
		s.log.Warn("handover: send failed", "err", err)
		return
	}

	ack, afds, err := k.RecvMsg()
	control.CloseFDs(afds)
	if err != nil || ack.Type != control.TypeHandoverAck {
		s.log.Warn("handover not acknowledged; keeping listeners", "err", err, "type", ack.Type)
		return
	}

	s.log.Info("handover acknowledged: draining", "active", s.reg.Active())
	s.reg.SetDraining()
	s.stopAccepting()
	s.closePeers()

	go func() {
		s.drain()
		s.exitOnce.Do(func() { close(s.exitCh) })
	}()
}

func (s *Server) collectSockets() ([]control.SockDesc, []int, error) {
	cfd, err := xnet.ListenerFD(s.ctrlLn)
	if err != nil {
		return nil, nil, fmt.Errorf("control listener fd: %w", err)
	}
	ufd, err := xnet.ListenerFD(s.upLn)
	if err != nil {
		return nil, nil, fmt.Errorf("upgrade listener fd: %w", err)
	}
	descs := []control.SockDesc{
		{Kind: control.SockControl, Listen: s.cfg.ControlSocket},
		{Kind: control.SockUpgrade, Listen: s.cfg.UpgradeSocket},
	}
	fds := []int{cfd, ufd}

	fdescs, ffds, err := s.fwd.Descriptors()
	if err != nil {
		return nil, nil, err
	}
	descs = append(descs, fdescs...)
	fds = append(fds, ffds...)

	if len(fds) > control.MaxRecvFDs {
		return nil, nil, errors.New("too many listeners to hand over")
	}
	return descs, fds, nil
}

func (s *Server) AdoptRunning() error {
	c, err := net.Dial("unix", s.cfg.UpgradeSocket)
	if err != nil {
		return fmt.Errorf("no running dpipe at %s: %w", s.cfg.UpgradeSocket, err)
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		_ = c.Close()
		return errors.New("upgrade socket is not a unix connection")
	}
	k := control.NewConn(uc)
	defer k.Close()

	if err := k.SendMsg(control.Msg{V: control.Version, Type: control.TypeHandoverRequest, ID: control.NewID()}, nil); err != nil {
		return err
	}
	m, fds, err := k.RecvMsg()
	if err != nil {
		return err
	}
	if m.Type != control.TypeHandover {
		control.CloseFDs(fds)
		if m.Type == control.TypeError {
			return fmt.Errorf("handover refused: %s", m.Error)
		}
		return fmt.Errorf("unexpected reply %q to handover_request", m.Type)
	}
	if len(fds) != len(m.Sockets) {
		control.CloseFDs(fds)
		return fmt.Errorf("handover: %d sockets but %d descriptors", len(m.Sockets), len(fds))
	}

	for i, d := range m.Sockets {
		ln, err := xnet.FileListener(fds[i])
		if err != nil {
			control.CloseFDs(fds[i+1:])
			return fmt.Errorf("handover: adopt %s: %w", d.Kind, err)
		}
		switch d.Kind {
		case control.SockControl, control.SockUpgrade:
			ul, ok := ln.(*net.UnixListener)
			if !ok {
				_ = ln.Close()
				control.CloseFDs(fds[i+1:])
				return fmt.Errorf("handover: %s listener is not a unix listener", d.Kind)
			}
			ul.SetUnlinkOnClose(false)
			if d.Kind == control.SockControl {
				s.ctrlLn = ul
			} else {
				s.upLn = ul
			}
		case control.SockListenForward:
			s.fwd.Adopt(d.ID, d.Listen, d.Target, ln)
		default:
			_ = ln.Close()
			control.CloseFDs(fds[i+1:])
			return fmt.Errorf("handover: unknown socket kind %q", d.Kind)
		}
	}
	if s.ctrlLn == nil || s.upLn == nil {
		return errors.New("handover: missing control or upgrade listener")
	}

	s.log.Info("adopted listeners from running instance", "sockets", len(m.Sockets))
	s.Start()

	if err := NotifyMainPID(os.Getpid()); err != nil {
		s.log.Warn("could not tell systemd the main pid moved", "err", err)
	}

	if err := k.SendMsg(control.Msg{V: control.Version, Type: control.TypeHandoverAck, ID: m.ID}, nil); err != nil {
		return fmt.Errorf("handover ack: %w", err)
	}
	return nil
}
