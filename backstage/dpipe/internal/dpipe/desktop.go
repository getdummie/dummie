package dpipe

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"dpipe/internal/control"
	"dpipe/internal/rdp"
	"dpipe/internal/xnet"
)

// The browser desktop is the console's counterpart for RDP. dproxy authorizes the
// upgrade the same way; past it, the websocket speaks RDCleanPath to IronRDP's
// in-browser client, which cannot open TLS itself but does run NLA itself.
//
// It has no config block: it is gated on rdp.enabled and reuses the console's
// session limits.
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

	_ = client.SetDeadline(time.Now().Add(s.cfg.RDP.HandshakeTimeout.Or(defaultRDPHandshakeTimeout)))
	backend, err := s.rdcleanpath(log, ws, m.Target)
	if err != nil {
		log.Warn("desktop backend connect failed", "target", m.Target, "err", err)
		ws.Close(wsCloseNormal, "desktop connect failed")
		return
	}
	defer func() { _ = backend.Close() }()
	_ = client.SetDeadline(time.Time{})

	log.Info("desktop session start", "target", m.Target)
	s.pumpDesktop(ws, backend)
	log.Info("desktop session end")
}

// rdcleanpath answers the browser's RDCleanPath request: X.224 and TLS with the
// guest, then the guest's response and certificate chain back to the browser,
// which runs NLA itself over the returned stream. The destination the browser
// names is ignored; dproxy already chose the target from the token.
func (s *Server) rdcleanpath(log *slog.Logger, ws *wsConn, target string) (net.Conn, error) {
	fail := func(code, status int64, err error) (net.Conn, error) {
		_ = ws.WriteMessage(opBinary, rdp.EncodeRDCleanPathError(code, status))
		return nil, err
	}

	op, payload, err := ws.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("read rdcleanpath request: %w", err)
	}
	if op != opBinary || len(payload) > rdp.MaxRDCleanPathRequest {
		return fail(rdp.RDCleanPathErrGeneral, 400, errors.New("not an rdcleanpath request"))
	}
	req, err := rdp.ParseRDCleanPathRequest(payload)
	if err != nil {
		return fail(rdp.RDCleanPathErrGeneral, 400, err)
	}
	cr, err := rdp.ReadConnectionRequest(bytes.NewReader(req.X224))
	if err != nil || len(cr.Raw) != len(req.X224) {
		return fail(rdp.RDCleanPathErrGeneral, 400, fmt.Errorf("unusable X.224 request: %v", err))
	}

	nc, err := net.DialTimeout("tcp", target, s.cfg.RDP.DialTimeout.Or(defaultDialTimeout))
	if err != nil {
		return fail(rdp.RDCleanPathErrGeneral, 502, err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = nc.Close()
		}
	}()
	_ = nc.SetDeadline(time.Now().Add(s.cfg.RDP.HandshakeTimeout.Or(defaultRDPHandshakeTimeout)))

	if _, err := nc.Write(cr.Raw); err != nil {
		return fail(rdp.RDCleanPathErrGeneral, 502, fmt.Errorf("write X.224 request: %w", err))
	}
	confirm, err := rdp.ReadFrame(nc)
	if err != nil {
		return fail(rdp.RDCleanPathErrNegotiation, 0, fmt.Errorf("read X.224 confirm: %w", err))
	}

	// Self-signed guest certificate; the browser binds CredSSP to this exact key.
	btls := tls.Client(nc, &tls.Config{InsecureSkipVerify: true})
	if err := btls.HandshakeContext(context.Background()); err != nil {
		return fail(rdp.RDCleanPathErrGeneral, 502, fmt.Errorf("backend tls handshake: %w", err))
	}
	var chain [][]byte
	for _, c := range btls.ConnectionState().PeerCertificates {
		chain = append(chain, c.Raw)
	}
	if err := ws.WriteMessage(opBinary, rdp.EncodeRDCleanPathResponse(target, confirm, chain)); err != nil {
		return nil, fmt.Errorf("write rdcleanpath response: %w", err)
	}

	_ = nc.SetDeadline(time.Time{})
	ok = true
	log.Debug("desktop rdcleanpath done", "target", target, "certs", len(chain))
	return btls, nil
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
