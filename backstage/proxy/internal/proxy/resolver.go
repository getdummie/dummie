package proxy

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"

	"golang.org/x/crypto/ssh"

	"proxy/internal/control"
	"proxy/internal/httpsniff"
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
	log     *slog.Logger
	router  *Router
	auth    *Authenticator
	console *ConsoleConfig
	users   map[string]sshPolicy // normalized "type base64" → policy
}

// NewResolver loads the SSH pubkey policy and captures the host map. auth may be
// nil, which Validate only allows when no host protects a port and no console is
// configured.
func NewResolver(log *slog.Logger, router *Router, auth *Authenticator, cfg *Config) (*Resolver, error) {
	r := &Resolver{log: log, router: router, auth: auth, console: cfg.Console, users: map[string]sshPolicy{}}
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
		log := r.log.With("id", m.ID, "client", m.ClientIP)
		req, err := resolveRequest(m)
		if err != nil {
			log.Warn("https: unusable request details", "host", m.Host, "err", err)
			return control.Msg{
				V: control.Version, Type: control.TypeResolved, ID: m.ID,
				Status: http.StatusBadRequest,
			}
		}

		// A console hostname is the proxy's own, exactly as on the plaintext
		// ingress: the VM host table is consulted first, so a name that is
		// genuinely a published VM always routes to that VM.
		if _, published := r.router.HostEntry(m.Host); !published {
			if vmHost, ok := consoleVMHost(r.router, r.console, m.Host); ok {
				return r.resolveConsole(log, m, vmHost, req)
			}
		}

		t, ok := r.router.HostBackend(m.Host)
		if !ok {
			log.Info("https deny: unknown host", "host", m.Host, "sni", m.SNI)
			return control.Msg{V: control.Version, Type: control.TypeResolved, ID: m.ID}
		}
		if v := authorizeRequest(log, r.router, r.auth, m.Host, req); !v.ok {
			return control.Msg{
				V: control.Version, Type: control.TypeResolved, ID: m.ID,
				Status: v.status, Location: v.location, SetCookie: v.setCookie,
			}
		}
		r.log.Info("https route", "id", m.ID, "host", m.Host, "sni", m.SNI,
			"client", m.ClientIP, "target", t)
		return control.Msg{
			V: control.Version, Type: control.TypeResolved, ID: m.ID,
			Authorized: true, Target: t,
		}

	default:
		return control.Err(m.ID, "unknown resolve kind "+m.Kind)
	}
}

// resolveConsole answers an http resolve that landed on a console hostname. The
// reply carries the same decision console_accept carries on the plaintext
// ingress; dpipe serves the terminal on the connection it already holds.
func (r *Resolver) resolveConsole(log *slog.Logger, m control.Msg, vmHost string, req *http.Request) control.Msg {
	host := httpsniff.NormalizeHost(m.Host)
	log = log.With("host", host, "vm_host", vmHost)

	v := authorizeConsole(log, r.router, r.console, r.auth, host, vmHost, req)
	if v.status != 0 {
		return control.Msg{
			V: control.Version, Type: control.TypeResolved, ID: m.ID,
			Status: v.status,
		}
	}
	log.Info("console: authorized", "sub", v.sub, "target", v.target, "remote_user", v.remoteUser)
	return control.Msg{
		V: control.Version, Type: control.TypeResolved, ID: m.ID,
		Authorized: true, Protocol: control.ProtoConsole,
		Target: v.target, RemoteUser: v.remoteUser, Sub: v.sub, WSKey: v.wsKey,
	}
}

// resolveRequest rebuilds the parts of a request the auth policy reads from the
// fields dpipe forwarded. A missing path becomes "/", which is never the callback
// and therefore never authenticates anyone.
func resolveRequest(m control.Msg) (*http.Request, error) {
	target := m.Path
	if target == "" {
		target = "/"
	}
	u, err := url.ParseRequestURI(target)
	if err != nil {
		return nil, err
	}
	req := &http.Request{Method: http.MethodGet, URL: u, Header: http.Header{}}
	req.Header.Set("Cookie", m.Cookie)
	req.Header.Set("Accept", m.Accept)
	req.Header.Set("Upgrade", m.Upgrade)
	req.Header.Set("Connection", m.Connection)
	req.Header.Set("Sec-WebSocket-Version", m.WSVersion)
	req.Header.Set("Sec-WebSocket-Key", m.WSKey)
	return req, nil
}
