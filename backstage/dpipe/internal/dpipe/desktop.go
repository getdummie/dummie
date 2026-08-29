package dpipe

import (
	"encoding/base64"
	"errors"
	"io"
	"net"
	"time"

	"dpipe/internal/control"
	"dpipe/internal/xnet"
)

// The browser desktop is the console's counterpart for RDP. dproxy authorizes the
// upgrade the same way; the difference is what sits on the far side of the
// websocket. Here dpipe brings up the guest leg — X.224, TLS and NLA — and then
// carries the decrypted RDP stream in binary frames, because the in-browser client
// runs in WebAssembly and cannot open a TLS connection of its own.
//
// It has no config block: it needs the same guest leg the native path does, so it
// is gated on rdp.enabled and reuses the console's session limits.
func (s *Server) handleDesktopAccept(p *control.Peer, m control.Msg, fds []int) {
	if len(fds) != 1 {
		control.CloseFDs(fds)
		_ = p.Send(control.Err(m.ID, "desktop_accept requires exactly 1 file descriptor"), nil)
		return
	}
	if !s.cfg.RDP.Enabled {
		control.CloseFDs(fds)
		s.log.Warn("desktop accept refused: rdp is not enabled in this dpipe config", "id", m.ID)
		_ = p.Send(control.Err(m.ID, "rdp not enabled"), nil)
		return
	}
	if s.reg.Draining() {
		control.CloseFDs(fds)
		_ = p.Send(control.Err(m.ID, "draining"), nil)
		return
	}
	pipelined, err := base64.StdEncoding.DecodeString(m.Prefix)
	if err != nil || len(pipelined) > control.MaxConsolePrefix {
		control.CloseFDs(fds)
		_ = p.Send(control.Err(m.ID, "desktop_accept: unusable prefix"), nil)
		return
	}
	client, err := xnet.FileConn(fds[0])
	if err != nil {
		_ = p.Send(control.Err(m.ID, err.Error()), nil)
		return
	}
	if !s.acquireConsole(m.Host) {
		s.log.Info("desktop refused: too many sessions for this vm", "id", m.ID, "host", m.Host, "sub", m.Sub)
		writeQuick(client, 503, "Service Unavailable")
		_ = client.Close()
		_ = p.Send(control.Err(m.ID, "too many desktop sessions for this host"), nil)
		return
	}
	_ = p.Send(control.OK(m.ID), nil)
	go s.serveDesktop(msgID(m), m, client, pipelined)
}

func (s *Server) serveDesktop(id string, m control.Msg, client net.Conn, pipelined []byte) {
	log := s.log.With("id", id, "protocol", control.ProtoDesktop, "client", m.ClientIP,
		"host", m.Host, "sub", m.Sub)

	defer s.releaseConsole(m.Host)
	s.reg.Add(id)
	defer s.reg.Done(id)

	ws, err := wsUpgrade(client, m.WSKey, pipelined)
	if err != nil {
		log.Warn("desktop handshake failed", "err", err)
		_ = client.Close()
		return
	}

	backend, err := s.dialGuestRDP(log, m.Target, m.RemoteUser, m.RemotePassword)
	if err != nil {
		log.Warn("desktop backend connect failed", "target", m.Target, "err", err)
		ws.Close(wsCloseInternalError, "desktop connect failed")
		return
	}
	defer func() { _ = backend.Close() }()

	log.Info("desktop session start", "target", m.Target, "remote_user", m.RemoteUser)
	s.pumpDesktop(ws, backend)
	log.Info("desktop session end")
}

// pumpDesktop moves bytes both ways until either side stops. Binary frames carry
// the RDP stream verbatim; text frames are ignored, leaving room for a control
// channel the way the console uses one for resize.
func (s *Server) pumpDesktop(ws *wsConn, backend net.Conn) {
	idle := s.cfg.Console.idleTimeout()
	done := make(chan struct{})

	go func() {
		defer close(done)
		buf := make([]byte, 32*1024)
		for {
			if idle > 0 {
				_ = backend.SetReadDeadline(time.Now().Add(idle))
			}
			n, err := backend.Read(buf)
			if n > 0 {
				if werr := ws.WriteMessage(opBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					ws.Close(wsCloseInternalError, "backend read failed")
					return
				}
				ws.Close(wsCloseNormal, "backend closed")
				return
			}
		}
	}()

	for {
		op, payload, err := ws.ReadMessage()
		if err != nil {
			break
		}
		if op != opBinary || len(payload) == 0 {
			continue
		}
		if _, err := backend.Write(payload); err != nil {
			break
		}
	}
	_ = backend.Close()
	<-done
	ws.Close(wsCloseNormal, "")
}
