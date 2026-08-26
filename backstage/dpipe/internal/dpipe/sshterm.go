package dpipe

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"dpipe/internal/control"
)

// serveSSH terminates SSH on an adopted raw socket: it authenticates the client
// by public key (the signature check is the library's; the proxy decides which
// VM that key may reach), dials the returned target as an SSH client with
// dpipe's own client key, and relays channels and requests both ways.
func (s *Server) serveSSH(p *control.Peer, id string, client net.Conn) {
	remoteIP := hostOnly(client.RemoteAddr())
	log := s.log.With("id", id, "protocol", control.ProtoSSH, "client", remoteIP)

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(cm ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			fp := ssh.FingerprintSHA256(key)
			ctx, cancel := context.WithTimeout(context.Background(), s.cfg.SSH.ResolveTimeout.Or(defaultResolveTimeout))
			defer cancel()

			rep, err := p.Request(ctx, control.Msg{
				V: control.Version, Type: control.TypeResolve, ID: control.NewID(),
				Kind:           control.KindSSH,
				SSHUser:        cm.User(),
				SSHPubKey:      strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))),
				SSHFingerprint: fp,
				ClientIP:       remoteIP,
			}, nil)
			if err != nil {
				// Retryable: the client may offer another key or reconnect.
				log.Warn("ssh resolve failed", "user", cm.User(), "fp", fp, "err", err)
				return nil, errors.New("authorization unavailable")
			}
			// A notice means the key is known but selected no VM. Auth has to
			// succeed for the client to see anything at all: an error here reaches
			// it as "Permission denied" with no text.
			if !rep.Authorized && rep.Notice != "" {
				log.Info("ssh no vm selected", "user", cm.User(), "fp", fp)
				return &ssh.Permissions{Extensions: map[string]string{"notice": rep.Notice}}, nil
			}
			if !rep.Authorized || rep.Target == "" {
				log.Info("ssh key not authorized", "user", cm.User(), "fp", fp)
				return nil, errors.New("key not authorized")
			}
			log.Info("ssh key authorized", "user", cm.User(), "fp", fp,
				"target", rep.Target, "remote_user", rep.RemoteUser)
			return &ssh.Permissions{Extensions: map[string]string{
				"target":      rep.Target,
				"remote_user": rep.RemoteUser,
			}}, nil
		},
	}
	cfg.AddHostKey(s.mat.hostSigner)

	sconn, chans, reqs, err := ssh.NewServerConn(client, cfg)
	if err != nil {
		log.Warn("ssh handshake failed", "err", err)
		_ = client.Close()
		return
	}
	defer sconn.Close()

	var target, remoteUser, notice string
	if sconn.Permissions != nil {
		target = sconn.Permissions.Extensions["target"]
		remoteUser = sconn.Permissions.Extensions["remote_user"]
		notice = sconn.Permissions.Extensions["notice"]
	}
	if target == "" && notice != "" {
		serveSSHNotice(log, chans, reqs, notice)
		return
	}
	if target == "" {
		log.Warn("ssh session without a target")
		return
	}
	// Not falling back to sconn.User(): the login name is the VM the client asked
	// for, not an account inside it.
	if remoteUser == "" {
		log.Warn("ssh session without a remote user")
		return
	}

	dialTimeout := s.cfg.SSH.DialTimeout.Or(defaultDialTimeout)
	nc, err := net.DialTimeout("tcp", target, dialTimeout)
	if err != nil {
		log.Warn("ssh backend dial failed", "target", target, "err", err)
		return
	}
	vconn, vchans, vreqs, err := ssh.NewClientConn(nc, target, &ssh.ClientConfig{
		User:            remoteUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(s.mat.clientSigner)},
		HostKeyCallback: s.mat.hostKeyCB,
		Timeout:         dialTimeout,
	})
	if err != nil {
		log.Warn("ssh backend handshake failed", "target", target, "err", err)
		_ = nc.Close()
		return
	}
	defer vconn.Close()

	s.reg.Add(id)
	defer s.reg.Done(id)
	log.Info("ssh session start", "target", target, "remote_user", remoteUser)
	relaySSH(log, sconn, chans, reqs, vconn, vchans, vreqs)
	log.Info("ssh session end")
}

// noticeWait bounds how long a session with nothing to route to stays open
// waiting for a channel to print on. A client that opens none -- ssh -N, a
// forward-only session -- has nowhere to be told anything and is just closed.
const noticeWait = 10 * time.Second

// serveSSHNotice prints text on the first session channel and hangs up. The
// caller closes the connection.
func serveSSHNotice(log *slog.Logger, chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request, notice string) {
	go ssh.DiscardRequests(reqs)
	// The client puts its terminal in raw mode once it has asked for a pty, so
	// bare newlines would stair-step.
	text := strings.ReplaceAll(notice, "\n", "\r\n")

	timer := time.NewTimer(noticeWait)
	defer timer.Stop()
	for {
		select {
		case nch, ok := <-chans:
			if !ok {
				return
			}
			if nch.ChannelType() != "session" {
				_ = nch.Reject(ssh.Prohibited, "no vm selected")
				continue
			}
			ch, chReqs, err := nch.Accept()
			if err != nil {
				log.Debug("ssh notice channel accept failed", "err", err)
				return
			}
			go func() {
				for req := range chReqs {
					if req.WantReply {
						_ = req.Reply(true, nil)
					}
				}
			}()
			_, _ = io.WriteString(ch, text)
			_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{1}))
			_ = ch.Close()
			return
		case <-timer.C:
			log.Debug("ssh notice: no session channel opened")
			return
		}
	}
}
