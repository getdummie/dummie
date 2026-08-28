package dpipe

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"

	"dpipe/internal/control"
	"dpipe/internal/xnet"
)

const (
	consoleCols = 80
	consoleRows = 24
)

const consoleTerm = "xterm-256color"

type consoleControl struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func (s *Server) handleConsoleAccept(p *control.Peer, m control.Msg, fds []int) {
	if len(fds) != 1 {
		control.CloseFDs(fds)
		_ = p.Send(control.Err(m.ID, "console_accept requires exactly 1 file descriptor"), nil)
		return
	}
	if !s.cfg.Console.Enabled {
		control.CloseFDs(fds)
		_ = p.Send(control.Err(m.ID, "console not enabled"), nil)
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
		_ = p.Send(control.Err(m.ID, "console_accept: unusable prefix"), nil)
		return
	}
	client, err := xnet.FileConn(fds[0])
	if err != nil {
		_ = p.Send(control.Err(m.ID, err.Error()), nil)
		return
	}
	if !s.acquireConsole(m.Host) {
		s.log.Info("console refused: too many sessions for this vm", "id", m.ID, "host", m.Host, "sub", m.Sub)
		writeQuick(client, 503, "Service Unavailable")
		_ = client.Close()
		_ = p.Send(control.Err(m.ID, "too many console sessions for this host"), nil)
		return
	}
	_ = p.Send(control.OK(m.ID), nil)
	go s.serveConsole(msgID(m), m, client, pipelined)
}

func (s *Server) acquireConsole(host string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.consoles[host] >= s.cfg.Console.maxPerHost() {
		return false
	}
	s.consoles[host]++
	return true
}

func (s *Server) releaseConsole(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := s.consoles[host] - 1; n > 0 {
		s.consoles[host] = n
		return
	}
	delete(s.consoles, host)
}

func writeQuick(w io.Writer, code int, reason string) {
	body := fmt.Sprintf("%d %s\n", code, reason)
	_, _ = fmt.Fprintf(w, "HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code, reason, len(body), body)
}

func (s *Server) serveConsole(id string, m control.Msg, client net.Conn, pipelined []byte) {
	log := s.log.With("id", id, "protocol", control.ProtoConsole, "client", m.ClientIP,
		"host", m.Host, "sub", m.Sub)

	defer s.releaseConsole(m.Host)
	s.reg.Add(id)
	defer s.reg.Done(id)

	ws, err := wsUpgrade(client, m.WSKey, pipelined)
	if err != nil {
		log.Warn("console handshake failed", "err", err)
		_ = client.Close()
		return
	}

	sshClient, err := s.dialConsoleSSH(m.Target, m.RemoteUser)
	if err != nil {
		log.Warn("console ssh dial failed", "target", m.Target, "err", err)
		ws.Close(wsCloseInternalError, "ssh connect failed")
		return
	}
	defer sshClient.Close()

	sess, err := sshClient.NewSession()
	if err != nil {
		log.Warn("console ssh session failed", "target", m.Target, "err", err)
		ws.Close(wsCloseInternalError, "ssh session failed")
		return
	}
	defer sess.Close()

	stdin, err := sess.StdinPipe()
	if err != nil {
		log.Warn("console stdin failed", "err", err)
		ws.Close(wsCloseInternalError, "ssh session failed")
		return
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		log.Warn("console stdout failed", "err", err)
		ws.Close(wsCloseInternalError, "ssh session failed")
		return
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		log.Warn("console stderr failed", "err", err)
		ws.Close(wsCloseInternalError, "ssh session failed")
		return
	}

	if err := sess.RequestPty(consoleTerm, consoleRows, consoleCols, ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 38400,
		ssh.TTY_OP_OSPEED: 38400,
	}); err != nil {
		log.Warn("console pty request failed", "err", err)
		ws.Close(wsCloseInternalError, "pty request failed")
		return
	}
	if err := sess.Shell(); err != nil {
		log.Warn("console shell failed", "err", err)
		ws.Close(wsCloseInternalError, "shell failed")
		return
	}

	log.Info("console session start", "target", m.Target, "remote_user", m.RemoteUser)

	var idle idleClock
	idle.touch()

	shellDone := make(chan struct{})
	go func() {
		defer close(shellDone)
		var out sync.WaitGroup
		for _, r := range []io.Reader{stdout, stderr} {
			out.Add(1)
			go func(r io.Reader) {
				defer out.Done()
				pumpToWS(ws, r, &idle)
			}(r)
		}
		_ = sess.Wait()
		out.Wait()
	}()

	stopIdle := make(chan struct{})
	defer close(stopIdle)
	idleOut := make(chan struct{})
	go func() {
		if idle.watch(s.cfg.Console.idleTimeout(), stopIdle) {
			close(idleOut)
		}
	}()

	readDone := make(chan error, 1)
	go func() { readDone <- pumpFromWS(log, ws, stdin, sess, &idle) }()

	select {
	case <-shellDone:
		log.Info("console session end", "reason", "shell exited")
		ws.Close(wsCloseNormal, "session ended")
	case err := <-readDone:
		log.Info("console session end", "reason", "client closed", "err", err)
		ws.Close(wsCloseNormal, "client closed")
	case <-idleOut:
		log.Info("console session end", "reason", "idle timeout")
		ws.Close(wsCloseNormal, "idle timeout")
	}
}

func (s *Server) dialConsoleSSH(target, remoteUser string) (*ssh.Client, error) {
	timeout := s.cfg.SSH.DialTimeout.Or(defaultDialTimeout)
	nc, err := net.DialTimeout("tcp", target, timeout)
	if err != nil {
		return nil, err
	}
	cc, chans, reqs, err := ssh.NewClientConn(nc, target, &ssh.ClientConfig{
		User:            remoteUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(s.mat.clientSigner)},
		HostKeyCallback: s.mat.hostKeyCB,
		Timeout:         timeout,
	})
	if err != nil {
		_ = nc.Close()
		return nil, err
	}
	return ssh.NewClient(cc, chans, reqs), nil
}

func pumpToWS(ws *wsConn, r io.Reader, idle *idleClock) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			idle.touch()
			if werr := ws.WriteMessage(opBinary, buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func pumpFromWS(log *slog.Logger, ws *wsConn, stdin io.Writer, sess *ssh.Session, idle *idleClock) error {
	for {
		op, payload, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		idle.touch()
		switch op {
		case opBinary:
			if _, err := stdin.Write(payload); err != nil {
				return err
			}
		case opText:
			var c consoleControl
			if err := json.Unmarshal(payload, &c); err != nil {
				log.Debug("console: unparsable control message")
				continue
			}
			if c.Type != "resize" || !validGeometry(c.Cols, c.Rows) {
				log.Debug("console: ignored control message", "type", c.Type)
				continue
			}
			if err := sess.WindowChange(c.Rows, c.Cols); err != nil {
				return err
			}
		}
	}
}

func validGeometry(cols, rows int) bool {
	return cols > 0 && cols <= 1000 && rows > 0 && rows <= 1000
}

type idleClock struct {
	last atomic.Int64
}

func (i *idleClock) touch() { i.last.Store(time.Now().UnixNano()) }

func (i *idleClock) watch(window time.Duration, stop <-chan struct{}) bool {
	tick := min(max(window/4, 100*time.Millisecond), 30*time.Second)
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return false
		case <-t.C:
			if time.Since(time.Unix(0, i.last.Load())) >= window {
				return true
			}
		}
	}
}
