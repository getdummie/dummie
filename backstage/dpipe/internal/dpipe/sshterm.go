package dpipe

import (
	"context"
	"errors"
	"net"
	"strings"

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

	var target, remoteUser string
	if sconn.Permissions != nil {
		target = sconn.Permissions.Extensions["target"]
		remoteUser = sconn.Permissions.Extensions["remote_user"]
	}
	if target == "" {
		log.Warn("ssh session without a target")
		return
	}
	if remoteUser == "" {
		remoteUser = sconn.User()
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
