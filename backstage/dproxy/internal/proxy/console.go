package proxy

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"dproxy/internal/control"
	"dproxy/internal/httpsniff"
)

// consoleSSHPort is the port a guest's sshd listens on. Guests are uniform, so
// this is not configurable.
const consoleSSHPort = 22

const defaultConsoleRemoteUser = "ubuntu"

// consoleVMHost maps a console hostname to the VM hostname behind it:
// "amazing-jepsen.shell.example.com" is the terminal for
// "amazing-jepsen.example.com". It reports false for anything that is not a
// console name or whose VM is not published on this host.
//
// A console name is a whole hostname of the proxy's own rather than a path on
// the VM's name, so nothing about it is ever forwarded to a guest: there is no
// request on that host a guest could see, and none it could answer.
func consoleVMHost(router *Router, cc *ConsoleConfig, host string) (string, bool) {
	if cc == nil {
		return "", false
	}
	host = httpsniff.NormalizeHost(host)
	// "<vm>.<label>.<domain>" needs a name, the label and at least one more
	// label to be a domain at all.
	labels := strings.Split(host, ".")
	if len(labels) < 3 || labels[1] != cc.label() {
		return "", false
	}
	vmHost := strings.Join(append([]string{labels[0]}, labels[2:]...), ".")
	if _, ok := router.HostEntry(vmHost); !ok {
		return "", false
	}
	return vmHost, true
}

// consoleAuth is what the console policy decided: where to open the shell, or a
// status to refuse with.
type consoleAuth struct {
	status     int // 0 when authorized
	target     string
	remoteUser string
	sub        string
	wsKey      string
}

// authorizeConsole applies the console policy for one request on a console
// hostname, which host must already be normalized to. It is the single decision
// point for both ingresses: the plaintext path hands the socket to dpipe with
// console_accept, the https path returns the same decision in the resolve reply.
//
// The signed token is the only credential: no cookie, no other path in. A cookie
// could not work here even if one were set, because the page that opens this
// websocket is served by the control server and a cross-site handshake does not
// carry one.
func authorizeConsole(log *slog.Logger, router *Router, cc *ConsoleConfig, a *Authenticator, host, vmHost string, req *http.Request) consoleAuth {
	if cc == nil || a == nil {
		log.Info("console: not configured")
		return consoleAuth{status: http.StatusNotFound}
	}
	wsKey, ok := websocketKey(req)
	if !ok {
		// The console host serves nothing but the websocket; the terminal page
		// itself lives on the control server.
		log.Info("console: not a websocket upgrade", "path", req.URL.Path)
		return consoleAuth{status: http.StatusNotFound}
	}

	// The token is a credential and lives in the query string: never log the
	// token, the query or the request URI. It is scoped to the console hostname,
	// not the VM's, so a session token for the VM cannot open a shell on it.
	sub, ok := a.VerifyConsole(req.URL.Query().Get("token"), host)
	if !ok {
		log.Info("console: token rejected")
		return consoleAuth{status: http.StatusUnauthorized}
	}

	entry, ok := router.HostEntry(vmHost)
	if !ok {
		log.Info("console: unknown host", "sub", sub)
		return consoleAuth{status: http.StatusBadGateway}
	}
	return consoleAuth{
		target:     net.JoinHostPort(entry.Host, strconv.Itoa(consoleSSHPort)),
		remoteUser: cc.remoteUser(),
		sub:        sub,
		wsKey:      wsKey,
	}
}

// handleConsole serves a console upgrade on the plaintext ingress. The connection
// is either handed to dpipe — which owns the SSH client key and the session — or
// answered here and closed; it is never forwarded to the guest.
func (p *Proxy) handleConsole(log *slog.Logger, client net.Conn, host, vmHost string, prefix []byte) {
	defer func() { _ = client.Close() }()

	host = httpsniff.NormalizeHost(host)
	log = log.With("host", host, "vm_host", vmHost)

	req, err := parseRequest(prefix)
	if err != nil {
		log.Warn("console: unparsable request", "err", err)
		writeQuick(client, 400)
		return
	}

	v := authorizeConsole(log, p.router, p.cfg.Console, p.auth, host, vmHost, req)
	if v.status != 0 {
		if v.status == http.StatusUnauthorized {
			writeUnauthorized(client)
		} else {
			writeQuick(client, v.status)
		}
		return
	}

	// Anything the client pipelined behind the upgrade request belongs to the
	// websocket stream, which dpipe owns from here on.
	pipelined := pipelinedBytes(prefix)
	if len(pipelined) > control.MaxConsolePrefix {
		log.Warn("console: too many bytes pipelined behind the upgrade", "bytes", len(pipelined))
		writeQuick(client, 400)
		return
	}

	id := control.NewID()
	msg := control.Msg{
		V: control.Version, Type: control.TypeConsoleAccept, ID: id,
		Protocol:   control.ProtoConsole,
		Host:       host,
		Target:     v.target,
		RemoteUser: v.remoteUser,
		Sub:        v.sub,
		WSKey:      v.wsKey,
		Prefix:     base64.StdEncoding.EncodeToString(pipelined),
		ClientIP:   hostOnly(client.RemoteAddr()),
	}
	log.Info("console: authorized", "id", id, "sub", v.sub, "target", v.target,
		"remote_user", msg.RemoteUser)

	if err := p.handoffConsoleAccept(msg, client); err != nil {
		log.Warn("console: handoff failed", "id", id, "err", err)
		writeQuick(client, 503)
		return
	}
	log.Debug("console: handed off", "id", id)
}

// pipelinedBytes returns the bytes of a sniffed prefix that follow the header
// block.
func pipelinedBytes(prefix []byte) []byte {
	if i := bytes.Index(prefix, []byte("\r\n\r\n")); i >= 0 {
		return prefix[i+4:]
	}
	return nil
}

// websocketKey validates the upgrade handshake and returns the client's
// Sec-WebSocket-Key, which dpipe needs to answer it.
func websocketKey(r *http.Request) (string, bool) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return "", false
	}
	if !headerHasToken(r.Header.Values("Connection"), "upgrade") {
		return "", false
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return "", false
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return "", false
	}
	// A 16-byte nonce, base64-encoded (RFC 6455 §4.1).
	if raw, err := base64.StdEncoding.DecodeString(key); err != nil || len(raw) != 16 {
		return "", false
	}
	return key, true
}

func headerHasToken(values []string, want string) bool {
	for _, v := range values {
		for _, tok := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), want) {
				return true
			}
		}
	}
	return false
}

func hostOnly(a net.Addr) string {
	if a == nil {
		return ""
	}
	h, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	return h
}
