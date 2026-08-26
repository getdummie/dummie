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

// Terminal geometry before the client's first resize.
const (
	consoleCols = 80
	consoleRows = 24
)

// consoleTerm is what the guest's shell is told it is talking to.
const consoleTerm = "xterm-256color"

// consoleControl is the JSON a client may send in a text frame. Text frames are
// client → server only; everything the guest produces goes out as binary.
type consoleControl struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// handleConsoleAccept adopts the browser's socket. Everything about who this is
// and which guest they may reach was decided by the proxy and is not revisited
// here; what is decided here is whether there is room to run it.
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
	// The slot is claimed before the handshake is answered, so a refusal is an
	// http error the browser can show rather than a websocket that opens and
	// immediately closes for no stated reason.
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

// acquireConsole takes a session slot for host, or reports that it is at its cap.
func (s *Server) acquireConsole(host string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.consoles[host] >= s.cfg.Console.maxPerHost() {
		return false
	}
	s.consoles[host]++
	return true
}

// releaseConsole returns a slot, dropping the counter entirely at zero.
func (s *Server) releaseConsole(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := s.consoles[host] - 1; n > 0 {
		s.consoles[host] = n
		return
	}
	delete(s.consoles, host)
}

// writeQuick writes a minimal HTTP/1.1 error response, for the window before a
// console connection becomes a websocket and stops speaking HTTP.
func writeQuick(w io.Writer, code int, reason string) {
	body := fmt.Sprintf("%d %s\n", code, reason)
	_, _ = fmt.Fprintf(w, "HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code, reason, len(body), body)
}

// serveConsole runs one browser terminal: it answers the websocket handshake the
// proxy validated, opens an SSH session to the guest with dpipe's client key, and
// pumps bytes between the two until either side ends or the session goes idle.
//
// The proxy has already verified the console token and resolved the guest; dpipe
// never sees the token or the secret it was signed with.
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

	// Guest → browser. stdout and stderr are interleaved on the same binary
	// stream, exactly as a terminal would see them.
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
		out.Wait() // let trailing output reach the browser before closing
	}()

	// Idle timeout: no traffic in either direction for the configured window
	// ends the session.
	stopIdle := make(chan struct{})
	defer close(stopIdle)
	idleOut := make(chan struct{})
	go func() {
		if idle.watch(s.cfg.Console.idleTimeout(), stopIdle) {
			close(idleOut)
		}
	}()

	// Browser → guest, on this goroutine: its exit is one of the two ways the
	// session ends.
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
	// Closing the websocket unblocks the reader; the deferred session and SSH
	// connection closes unblock the writers, so nothing is left running and no
	// descriptor is left open.
}

// dialConsoleSSH connects to the guest's sshd with dpipe's client key, under the
// same host-key policy as the SSH ingress.
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

// pumpToWS forwards guest output as binary frames.
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

// pumpFromWS forwards browser input: binary frames are raw stdin, text frames are
// JSON control messages. An unparsable or unknown control message is ignored
// rather than killing a working terminal.
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

// validGeometry rejects geometries no terminal has, which a guest's sshd would
// reject anyway.
func validGeometry(cols, rows int) bool {
	return cols > 0 && cols <= 1000 && rows > 0 && rows <= 1000
}

// idleClock tracks the last time either direction moved bytes.
type idleClock struct {
	last atomic.Int64
}

func (i *idleClock) touch() { i.last.Store(time.Now().UnixNano()) }

// watch reports true when the idle window elapses with no activity, and false
// when stop is closed first.
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
