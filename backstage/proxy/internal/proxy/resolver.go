package proxy

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"

	"golang.org/x/crypto/ssh"

	"proxy/internal/control"
)

// sshPolicy is one entry of the pubkey → {target, remote_user} policy.
type sshPolicy struct {
	target      string
	remoteUser  string
	fingerprint string
}

// Resolver answers resolve requests from dpipe. It is the only place where
// authorization decisions are made; dpipe never sees the policy.
type Resolver struct {
	log    *slog.Logger
	router *Router
	users  map[string]sshPolicy // normalized "type base64" → policy
}

// NewResolver loads the SSH pubkey policy and captures the host map.
func NewResolver(log *slog.Logger, router *Router, cfg *Config) (*Resolver, error) {
	r := &Resolver{log: log, router: router, users: map[string]sshPolicy{}}
	if cfg.SSH == nil {
		return r, nil
	}
	for i, u := range cfg.SSH.Users {
		raw := u.PubKey
		if u.PubKeyFile != "" {
			b, err := os.ReadFile(u.PubKeyFile)
			if err != nil {
				return nil, fmt.Errorf("ssh.users[%d].pubkey_file: %w", i, err)
			}
			raw = string(b)
		}
		key, err := ParseAuthorizedKey(raw)
		if err != nil {
			return nil, fmt.Errorf("ssh.users[%d]: %w", i, err)
		}
		norm := NormalizeKey(key)
		if prev, dup := r.users[norm]; dup {
			return nil, fmt.Errorf("ssh.users[%d]: duplicate public key (already maps to %s)", i, prev.target)
		}
		r.users[norm] = sshPolicy{
			target:      u.Target,
			remoteUser:  u.RemoteUser,
			fingerprint: ssh.FingerprintSHA256(key),
		}
	}
	return r, nil
}

// ParseAuthorizedKey parses one authorized_keys line.
func ParseAuthorizedKey(s string) (ssh.PublicKey, error) {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(s))
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	return key, nil
}

// NormalizeKey renders a public key as "type base64" — the authorized_keys form
// without the trailing comment — so map lookups ignore comments and whitespace.
func NormalizeKey(key ssh.PublicKey) string {
	return key.Type() + " " + base64.StdEncoding.EncodeToString(key.Marshal())
}

// Handle answers a resolve request. It fails closed: anything unknown is
// unauthorized.
func (r *Resolver) Handle(m control.Msg) control.Msg {
	switch m.Kind {
	case control.KindSSH:
		key, err := ParseAuthorizedKey(m.SSHPubKey)
		if err != nil {
			r.log.Warn("resolve ssh: unparsable key", "id", m.ID, "client", m.ClientIP, "err", err)
			return control.Msg{V: control.Version, Type: control.TypeResolved, ID: m.ID}
		}
		if u, ok := r.users[NormalizeKey(key)]; ok {
			r.log.Info("ssh authorize", "id", m.ID, "user", m.SSHUser, "fp", u.fingerprint,
				"client", m.ClientIP, "target", u.target, "remote_user", u.remoteUser)
			return control.Msg{
				V: control.Version, Type: control.TypeResolved, ID: m.ID,
				Authorized: true, Target: u.target, RemoteUser: u.remoteUser,
			}
		}
		r.log.Info("ssh deny: unknown key", "id", m.ID, "user", m.SSHUser,
			"fp", ssh.FingerprintSHA256(key), "client", m.ClientIP)
		return control.Msg{V: control.Version, Type: control.TypeResolved, ID: m.ID}

	case control.KindHTTP:
		if t, ok := r.router.HostBackend(m.Host); ok {
			r.log.Info("https route", "id", m.ID, "host", m.Host, "sni", m.SNI,
				"client", m.ClientIP, "target", t)
			return control.Msg{
				V: control.Version, Type: control.TypeResolved, ID: m.ID,
				Authorized: true, Target: t,
			}
		}
		r.log.Info("https deny: unknown host", "id", m.ID, "host", m.Host, "sni", m.SNI, "client", m.ClientIP)
		return control.Msg{V: control.Version, Type: control.TypeResolved, ID: m.ID}

	default:
		return control.Err(m.ID, "unknown resolve kind "+m.Kind)
	}
}
