package dpipe

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"

	"dpipe/internal/control"
	"dpipe/internal/xnet"
)

// The agent tab is a console without a pty: dpipe runs `dinit agent attach` in
// the guest and carries each websocket message across as one frame of
// [opcode byte][big-endian uint32 length][payload], which dinit speaks back.
const (
	agentCommand  = "/sbin/dinit agent attach"
	agentMaxFrame = 64 << 20
)

func agentHostKey(host string) string { return "agent:" + host }

func (s *Server) handleAgentAccept(p *control.Peer, m control.Msg, fds []int) {
	if len(fds) != 1 {
		control.CloseFDs(fds)
		_ = p.Send(control.Err(m.ID, "agent_accept requires exactly 1 file descriptor"), nil)
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
		_ = p.Send(control.Err(m.ID, "agent_accept: unusable prefix"), nil)
		return
	}
	client, err := xnet.FileConn(fds[0])
	if err != nil {
		_ = p.Send(control.Err(m.ID, err.Error()), nil)
		return
	}
	if !s.acquireConsole(agentHostKey(m.Host)) {
		writeQuick(client, 503, "Service Unavailable")
		_ = client.Close()
		_ = p.Send(control.Err(m.ID, "too many agent sessions for this host"), nil)
		return
	}
	_ = p.Send(control.OK(m.ID), nil)
	go s.serveAgent(msgID(m), m, client, pipelined)
}

func (s *Server) serveTLSAgent(log *slog.Logger, id string, rep control.Msg, tc net.Conn, host, clientIP string, prefix []byte) {
	if !s.cfg.Console.Enabled {
		log.Warn("agent over tls but console is not enabled", "host", host)
		writeQuickTLS(tc, 404)
		_ = tc.Close()
		return
	}
	if !s.acquireConsole(agentHostKey(host)) {
		writeQuickTLS(tc, 503)
		_ = tc.Close()
		return
	}
	s.serveAgent(id, control.Msg{
		Host: host, Target: rep.Target, RemoteUser: rep.RemoteUser,
		Sub: rep.Sub, WSKey: rep.WSKey, ClientIP: clientIP,
	}, tc, pipelinedBytes(prefix))
}

// agentTLD is what the guest needs to find the fleet's llm proxy at
// llm.int.<tld>: the console host is <vm>.<label>.<tld>.
func agentTLD(host string) string {
	labels := strings.Split(host, ".")
	if len(labels) < 3 {
		return ""
	}
	return strings.Join(labels[2:], ".")
}

func (s *Server) serveAgent(id string, m control.Msg, client net.Conn, pipelined []byte) {
	log := s.log.With("id", id, "protocol", control.ProtoAgent, "client", m.ClientIP,
		"host", m.Host, "sub", m.Sub)

	defer s.releaseConsole(agentHostKey(m.Host))
	s.reg.Add(id)
	defer s.reg.Done(id)

	ws, err := wsUpgrade(client, m.WSKey, pipelined)
	if err != nil {
		log.Warn("agent handshake failed", "err", err)
		_ = client.Close()
		return
	}

	sshClient, err := s.dialConsoleSSH(m.Target, m.RemoteUser)
	if err != nil {
		log.Warn("agent ssh dial failed", "target", m.Target, "err", err)
		agentError(ws, "ssh_failed", "could not reach the vm over ssh")
		ws.Close(wsCloseInternalError, "ssh connect failed")
		return
	}
	defer sshClient.Close()

	sess, err := sshClient.NewSession()
	if err != nil {
		log.Warn("agent ssh session failed", "err", err)
		ws.Close(wsCloseInternalError, "ssh session failed")
		return
	}
	defer sess.Close()
	stdin, err := sess.StdinPipe()
	if err != nil {
		ws.Close(wsCloseInternalError, "ssh session failed")
		return
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		ws.Close(wsCloseInternalError, "ssh session failed")
		return
	}
	cmd := agentCommand
	if tld := agentTLD(m.Host); tld != "" && validTLD(tld) {
		cmd += " --tld " + tld
	}
	if err := sess.Start(cmd); err != nil {
		log.Warn("agent start failed", "err", err)
		ws.Close(wsCloseInternalError, "agent start failed")
		return
	}
	log.Info("agent session start", "target", m.Target, "remote_user", m.RemoteUser)

	var idle idleClock
	idle.touch()

	outDone := make(chan error, 1)
	go func() { outDone <- pumpAgentToWS(ws, bufio.NewReader(stdout), &idle) }()

	stopIdle := make(chan struct{})
	defer close(stopIdle)
	idleOut := make(chan struct{})
	go func() {
		if idle.watch(s.cfg.Console.idleTimeout(), stopIdle) {
			close(idleOut)
		}
	}()

	readDone := make(chan error, 1)
	go func() { readDone <- pumpWSToAgent(ws, stdin, &idle) }()

	select {
	case err := <-outDone:
		if errors.Is(err, errNotAgent) {
			log.Info("agent session end", "reason", "guest dinit has no agent")
			agentError(ws, "agent_unsupported", "this vm's dinit is too old for the agent; run `sudo /sbin/dinit upgrade` in its console, or recreate the vm")
		} else {
			log.Info("agent session end", "reason", "agent exited", "err", err)
		}
		ws.Close(wsCloseNormal, "agent ended")
	case err := <-readDone:
		log.Info("agent session end", "reason", "client closed", "err", err)
		ws.Close(wsCloseNormal, "client closed")
	case <-idleOut:
		log.Info("agent session end", "reason", "idle timeout")
		ws.Close(wsCloseNormal, "idle timeout")
	}
}

var errNotAgent = errors.New("the guest did not answer in agent frames")

// validTLD keeps the tld safe to put on a shell command line.
func validTLD(tld string) bool {
	for _, r := range tld {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

func agentError(ws *wsConn, code, msg string) {
	_ = ws.WriteMessage(opText, []byte(fmt.Sprintf(`{"t":"error","code":%s,"message":%s}`,
		strconv.Quote(code), strconv.Quote(msg))))
}

// pumpAgentToWS reports errNotAgent when the very first bytes are not a frame:
// an old dinit answers `agent attach` with its usage text instead.
func pumpAgentToWS(ws *wsConn, r *bufio.Reader, idle *idleClock) error {
	first := true
	for {
		var hdr [5]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if first {
				return errNotAgent
			}
			return err
		}
		op, n := hdr[0], binary.BigEndian.Uint32(hdr[1:])
		if (op != opText && op != opBinary) || n > agentMaxFrame {
			if first {
				return errNotAgent
			}
			return fmt.Errorf("bad agent frame: op %d, %d bytes", op, n)
		}
		first = false
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return err
		}
		idle.touch()
		if err := ws.WriteMessage(op, buf); err != nil {
			return err
		}
	}
}

func pumpWSToAgent(ws *wsConn, w io.Writer, idle *idleClock) error {
	for {
		op, payload, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		if op != opText && op != opBinary {
			continue
		}
		idle.touch()
		hdr := make([]byte, 5, 5+len(payload))
		hdr[0] = op
		binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
		if _, err := w.Write(append(hdr, payload...)); err != nil {
			return err
		}
	}
}
