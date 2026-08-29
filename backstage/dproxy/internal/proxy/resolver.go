package proxy

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"

	"dproxy/internal/control"
	"dproxy/internal/httpsniff"
	"dproxy/internal/ntlm"
)

type sshPolicy struct {
	target      string
	remoteUser  string
	fingerprint string
}

type sshUserKey struct {
	pubkey string
	vmName string
}

// rdpPolicy is the per-VM remote-desktop credential. The NT hash verifies the
// caller; remoteUser/remotePassword are what dpipe presents to the guest.
type rdpPolicy struct {
	vmName         string
	ntHash         []byte
	target         string
	remoteUser     string
	remotePassword string
}

type Resolver struct {
	log     *slog.Logger
	router  *Router
	auth    *Authenticator
	console *ConsoleConfig
	desktop *DesktopConfig
	users   map[sshUserKey]sshPolicy
	owned map[string][]string
	rdp   map[string]rdpPolicy
}

func NewResolver(log *slog.Logger, router *Router, auth *Authenticator, cfg *Config) (*Resolver, error) {
	r := &Resolver{
		log: log, router: router, auth: auth, console: cfg.Console, desktop: cfg.Desktop,
		users: map[sshUserKey]sshPolicy{}, owned: map[string][]string{},
		rdp: map[string]rdpPolicy{},
	}
	if cfg.RDP != nil {
		for i, u := range cfg.RDP.Users {
			h, err := hex.DecodeString(u.NTHash)
			if err != nil {
				return nil, fmt.Errorf("rdp.users[%d].nt_hash: %w", i, err)
			}
			// RDP login names are matched case-insensitively, as Windows does.
			r.rdp[strings.ToLower(u.VMName)] = rdpPolicy{
				vmName:         u.VMName,
				ntHash:         h,
				target:         u.Target,
				remoteUser:     u.RemoteUser,
				remotePassword: u.RemotePassword,
			}
		}
	}
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
		id := sshUserKey{pubkey: NormalizeKey(key), vmName: u.VMName}
		if prev, dup := r.users[id]; dup {
			return nil, fmt.Errorf("ssh.users[%d]: public key already maps to vm %q (%s)", i, u.VMName, prev.target)
		}
		r.users[id] = sshPolicy{
			target:      u.Target,
			remoteUser:  u.RemoteUser,
			fingerprint: ssh.FingerprintSHA256(key),
		}
		r.owned[id.pubkey] = append(r.owned[id.pubkey], u.VMName)
	}
	return r, nil
}

func sshVMNotice(requested string, owned []string) string {
	var b strings.Builder
	if isPrintableName(requested) {
		fmt.Fprintf(&b, "No VM named %q for this key.\n\n", requested)
	} else {
		b.WriteString("No VM selected.\n\n")
	}
	b.WriteString("This key can reach:\n")
	for i, name := range owned {
		if b.Len()+len(name)+len(sshNoticeFooter)+32 > control.MaxNotice {
			fmt.Fprintf(&b, "  ... and %d more\n", len(owned)-i)
			break
		}
		fmt.Fprintf(&b, "  %s\n", name)
	}
	b.WriteString(sshNoticeFooter)
	return b.String()
}

const sshNoticeFooter = "\nName the one you want as the login:\n  ssh <vm-name>@<this-host>\n"

func isPrintableName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func ParseAuthorizedKey(s string) (ssh.PublicKey, error) {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(s))
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	return key, nil
}

func NormalizeKey(key ssh.PublicKey) string {
	return key.Type() + " " + base64.StdEncoding.EncodeToString(key.Marshal())
}

func (r *Resolver) Handle(m control.Msg) control.Msg {
	switch m.Kind {
	case control.KindSSH:
		key, err := ParseAuthorizedKey(m.SSHPubKey)
		if err != nil {
			r.log.Warn("resolve ssh: unparsable key", "id", m.ID, "client", m.ClientIP, "err", err)
			return control.Msg{V: control.Version, Type: control.TypeResolved, ID: m.ID}
		}
		norm := NormalizeKey(key)
		if u, ok := r.users[sshUserKey{pubkey: norm, vmName: m.SSHUser}]; ok {
			r.log.Info("ssh authorize", "id", m.ID, "vm", m.SSHUser, "fp", u.fingerprint,
				"client", m.ClientIP, "target", u.target, "remote_user", u.remoteUser)
			return control.Msg{
				V: control.Version, Type: control.TypeResolved, ID: m.ID,
				Authorized: true, Target: u.target, RemoteUser: u.remoteUser,
			}
		}
		if owned := r.owned[norm]; len(owned) > 0 {
			r.log.Info("ssh no vm selected", "id", m.ID, "vm", m.SSHUser,
				"fp", ssh.FingerprintSHA256(key), "client", m.ClientIP, "owned", len(owned))
			return control.Msg{
				V: control.Version, Type: control.TypeResolved, ID: m.ID,
				Notice: sshVMNotice(m.SSHUser, owned),
			}
		}
		r.log.Info("ssh deny: unknown key", "id", m.ID, "vm", m.SSHUser,
			"fp", ssh.FingerprintSHA256(key), "client", m.ClientIP)
		return control.Msg{V: control.Version, Type: control.TypeResolved, ID: m.ID}

	case control.KindRDP:
		return r.resolveRDP(m)

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

		if _, published := r.router.HostEntry(m.Host); !published {
			if k, ok := desktopKind(r.desktop); ok {
				if vmHost, ok := labelledVMHost(r.router, k.label, m.Host); ok {
					return r.resolveSession(log, k, m, vmHost, req)
				}
			}
			if k, ok := consoleKind(r.console); ok {
				if vmHost, ok := labelledVMHost(r.router, k.label, m.Host); ok {
					return r.resolveSession(log, k, m, vmHost, req)
				}
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

// resolveSession answers a console or desktop upgrade arriving over https. It is
// the same decision the plaintext ingress makes in handleSession; only who writes
// the response differs, which is why the verdict travels back to dpipe instead.
func (r *Resolver) resolveSession(log *slog.Logger, k sessionKind, m control.Msg, vmHost string, req *http.Request) control.Msg {
	host := httpsniff.NormalizeHost(m.Host)
	log = log.With("host", host, "vm_host", vmHost)

	v := authorizeSession(log, r.router, r.auth, k, host, vmHost, req)
	if v.status != 0 {
		return control.Msg{
			V: control.Version, Type: control.TypeResolved, ID: m.ID,
			Status: v.status,
		}
	}
	log.Info(k.name+": authorized", "sub", v.sub, "target", v.target, "remote_user", v.remoteUser)
	return control.Msg{
		V: control.Version, Type: control.TypeResolved, ID: m.ID,
		Authorized: true, Protocol: k.protocol,
		Target: v.target, RemoteUser: v.remoteUser, RemotePassword: v.remotePassword,
		Sub: v.sub, WSKey: v.wsKey,
	}
}

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

// resolveRDP verifies an NTLMv2 response against the NT hash for the VM the login
// name selects. The name says which VM; the response proves the caller holds that
// VM's issued password. Neither alone is enough, which is what stops one tenant
// reaching another's desktop.
func (r *Resolver) resolveRDP(m control.Msg) control.Msg {
	deny := control.Msg{V: control.Version, Type: control.TypeResolved, ID: m.ID}
	log := r.log.With("id", m.ID, "client", m.ClientIP)

	challenge, err := base64.StdEncoding.DecodeString(m.RDPChallenge)
	if err != nil || len(challenge) != 8 {
		log.Warn("rdp deny: unusable challenge", "user", m.RDPUser)
		return deny
	}
	response, err := base64.StdEncoding.DecodeString(m.RDPNTResponse)
	if err != nil || len(response) > control.MaxNTLMBlob {
		log.Warn("rdp deny: unusable nt response", "user", m.RDPUser)
		return deny
	}

	u, ok := r.rdp[strings.ToLower(m.RDPUser)]
	if !ok {
		log.Info("rdp deny: unknown vm", "user", m.RDPUser)
		return deny
	}
	var sc [8]byte
	copy(sc[:], challenge)
	key, ok := ntlm.VerifyNTLMv2(u.ntHash, m.RDPUser, m.RDPDomain, sc, response)
	if !ok {
		log.Info("rdp deny: bad password", "vm", u.vmName)
		return deny
	}

	log.Info("rdp authorize", "vm", u.vmName, "target", u.target, "remote_user", u.remoteUser)
	return control.Msg{
		V: control.Version, Type: control.TypeResolved, ID: m.ID,
		Authorized:     true,
		Target:         u.target,
		RemoteUser:     u.remoteUser,
		RemotePassword: u.remotePassword,
		RDPSessionKey:  base64.StdEncoding.EncodeToString(key),
	}
}
