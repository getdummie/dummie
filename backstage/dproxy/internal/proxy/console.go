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

const consoleSSHPort = 22

const desktopRDPPort = 3389

const defaultConsoleRemoteUser = "ubuntu"

// sessionKind describes one hostname-labelled websocket entry point onto a VM.
// The console and the browser desktop differ only in the label they answer on,
// the audience their token must carry, and which guest port they land on, so
// they share every step of the authorization.
type sessionKind struct {
	name           string
	label          string
	audPrefix      string
	port           int
	remoteUser     string
	remotePassword string
	protocol       string
	acceptType     string
}

func (p *Proxy) consoleKind() (sessionKind, bool) {
	return consoleKind(p.cfg.Console)
}

func (p *Proxy) desktopKind() (sessionKind, bool) {
	return desktopKind(p.cfg.Desktop)
}

func desktopKind(d *DesktopConfig) (sessionKind, bool) {
	if d == nil {
		return sessionKind{}, false
	}
	return sessionKind{
		name:           "desktop",
		label:          d.label(),
		audPrefix:      desktopAudPrefix,
		port:           desktopRDPPort,
		remoteUser:     d.remoteUser(),
		remotePassword: d.RemotePassword,
		protocol:       control.ProtoDesktop,
		acceptType:     control.TypeDesktopAccept,
	}, true
}

func consoleKind(c *ConsoleConfig) (sessionKind, bool) {
	if c == nil {
		return sessionKind{}, false
	}
	return sessionKind{
		name:       "console",
		label:      c.label(),
		audPrefix:  consoleAudPrefix,
		port:       consoleSSHPort,
		remoteUser: c.remoteUser(),
		protocol:   control.ProtoConsole,
		acceptType: control.TypeConsoleAccept,
	}, true
}

// labelledVMHost maps "one.shell.vm.local" to the published host "one.vm.local".
// The label must sit in the second position, so a guest cannot claim one by
// naming itself after it.
func labelledVMHost(router *Router, label, host string) (string, bool) {
	host = httpsniff.NormalizeHost(host)
	labels := strings.Split(host, ".")
	if len(labels) < 3 || labels[1] != label {
		return "", false
	}
	vmHost := strings.Join(append([]string{labels[0]}, labels[2:]...), ".")
	if _, ok := router.HostEntry(vmHost); !ok {
		return "", false
	}
	return vmHost, true
}

func consoleVMHost(router *Router, cc *ConsoleConfig, host string) (string, bool) {
	k, ok := consoleKind(cc)
	if !ok {
		return "", false
	}
	return labelledVMHost(router, k.label, host)
}

func (p *Proxy) consoleVMHost(host string) (string, bool) {
	return consoleVMHost(p.router, p.cfg.Console, host)
}

func desktopVMHost(router *Router, d *DesktopConfig, host string) (string, bool) {
	k, ok := desktopKind(d)
	if !ok {
		return "", false
	}
	return labelledVMHost(router, k.label, host)
}

func (p *Proxy) desktopVMHost(host string) (string, bool) {
	return desktopVMHost(p.router, p.cfg.Desktop, host)
}

type sessionAuth struct {
	status         int
	target         string
	remoteUser     string
	remotePassword string
	sub            string
	wsKey          string
}

func authorizeSession(log *slog.Logger, router *Router, a *Authenticator, k sessionKind, host, vmHost string, req *http.Request) sessionAuth {
	if a == nil {
		log.Info(k.name + ": not configured")
		return sessionAuth{status: http.StatusNotFound}
	}
	wsKey, ok := websocketKey(req)
	if !ok {
		log.Info(k.name+": not a websocket upgrade", "path", req.URL.Path)
		return sessionAuth{status: http.StatusNotFound}
	}

	// The audience prefix is what keeps a console token from opening a desktop
	// and vice versa, even though both name the same VM.
	sub, ok := a.verifyLabelled(req.URL.Query().Get("token"), k.audPrefix, host)
	if !ok {
		log.Info(k.name + ": token rejected")
		return sessionAuth{status: http.StatusUnauthorized}
	}

	entry, ok := router.HostEntry(vmHost)
	if !ok {
		log.Info(k.name+": unknown host", "sub", sub)
		return sessionAuth{status: http.StatusBadGateway}
	}
	return sessionAuth{
		target:         net.JoinHostPort(entry.Host, strconv.Itoa(k.port)),
		remoteUser:     k.remoteUser,
		remotePassword: k.remotePassword,
		sub:            sub,
		wsKey:          wsKey,
	}
}

func authorizeConsole(log *slog.Logger, router *Router, cc *ConsoleConfig, a *Authenticator, host, vmHost string, req *http.Request) sessionAuth {
	k, ok := consoleKind(cc)
	if !ok {
		log.Info("console: not configured")
		return sessionAuth{status: http.StatusNotFound}
	}
	return authorizeSession(log, router, a, k, host, vmHost, req)
}

func (p *Proxy) handleConsole(log *slog.Logger, client net.Conn, host, vmHost string, prefix []byte) {
	k, ok := p.consoleKind()
	if !ok {
		_ = client.Close()
		return
	}
	p.handleSession(log, k, client, host, vmHost, prefix)
}

func (p *Proxy) handleDesktop(log *slog.Logger, client net.Conn, host, vmHost string, prefix []byte) {
	k, ok := p.desktopKind()
	if !ok {
		_ = client.Close()
		return
	}
	p.handleSession(log, k, client, host, vmHost, prefix)
}

func (p *Proxy) handleSession(log *slog.Logger, k sessionKind, client net.Conn, host, vmHost string, prefix []byte) {
	defer func() { _ = client.Close() }()

	host = httpsniff.NormalizeHost(host)
	log = log.With("host", host, "vm_host", vmHost)

	req, err := parseRequest(prefix)
	if err != nil {
		log.Warn(k.name+": unparsable request", "err", err)
		writeQuick(client, 400)
		return
	}

	v := authorizeSession(log, p.router, p.auth, k, host, vmHost, req)
	if v.status != 0 {
		if v.status == http.StatusUnauthorized {
			writeUnauthorized(client)
		} else {
			writeQuick(client, v.status)
		}
		return
	}

	pipelined := pipelinedBytes(prefix)
	if len(pipelined) > control.MaxConsolePrefix {
		log.Warn(k.name+": too many bytes pipelined behind the upgrade", "bytes", len(pipelined))
		writeQuick(client, 400)
		return
	}

	id := control.NewID()
	msg := control.Msg{
		V: control.Version, Type: k.acceptType, ID: id,
		Protocol:       k.protocol,
		Host:           host,
		Target:         v.target,
		RemoteUser:     v.remoteUser,
		RemotePassword: v.remotePassword,
		Sub:            v.sub,
		WSKey:          v.wsKey,
		Prefix:         base64.StdEncoding.EncodeToString(pipelined),
		ClientIP:       hostOnly(client.RemoteAddr()),
	}
	log.Info(k.name+": authorized", "id", id, "sub", v.sub, "target", v.target,
		"remote_user", msg.RemoteUser)

	if err := p.handoffSessionAccept(msg, client); err != nil {
		log.Warn(k.name+": handoff failed", "id", id, "err", err)
		writeQuick(client, 503)
		return
	}
	log.Debug(k.name+": handed off", "id", id)
}

func pipelinedBytes(prefix []byte) []byte {
	if i := bytes.Index(prefix, []byte("\r\n\r\n")); i >= 0 {
		return prefix[i+4:]
	}
	return nil
}

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
