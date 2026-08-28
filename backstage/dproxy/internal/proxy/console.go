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

const defaultConsoleRemoteUser = "ubuntu"

func consoleVMHost(router *Router, cc *ConsoleConfig, host string) (string, bool) {
	if cc == nil {
		return "", false
	}
	host = httpsniff.NormalizeHost(host)
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

type consoleAuth struct {
	status     int
	target     string
	remoteUser string
	sub        string
	wsKey      string
}

func authorizeConsole(log *slog.Logger, router *Router, cc *ConsoleConfig, a *Authenticator, host, vmHost string, req *http.Request) consoleAuth {
	if cc == nil || a == nil {
		log.Info("console: not configured")
		return consoleAuth{status: http.StatusNotFound}
	}
	wsKey, ok := websocketKey(req)
	if !ok {
		log.Info("console: not a websocket upgrade", "path", req.URL.Path)
		return consoleAuth{status: http.StatusNotFound}
	}

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
