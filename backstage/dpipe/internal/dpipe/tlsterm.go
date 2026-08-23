package dpipe

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"dpipe/internal/control"
	"dpipe/internal/httpsniff"
)

// serveTLS terminates TLS on an adopted raw socket, sniffs the decrypted HTTP
// Host, asks the proxy where to send it, and copies the decrypted stream to the
// backend.
//
// TLS is terminated here (not in the proxy) because a live TLS session cannot be
// fd-passed: its state lives in userspace and must therefore live in the process
// that is not redeployed.
func (s *Server) serveTLS(p *control.Peer, id string, client net.Conn) {
	remoteIP := hostOnly(client.RemoteAddr())
	log := s.log.With("id", id, "protocol", control.ProtoTLS, "client", remoteIP)

	cfg := &tls.Config{
		MinVersion: s.mat.tlsMin,
		// HTTP/1.1 only in v1: no h2.
		NextProtos:     []string{"http/1.1"},
		GetCertificate: s.certForSNI,
	}
	tc := tls.Server(client, cfg)

	hsCtx, cancel := context.WithTimeout(context.Background(), s.cfg.TLS.DialTimeout.Or(defaultDialTimeout))
	defer cancel()
	if err := tc.HandshakeContext(hsCtx); err != nil {
		log.Warn("tls handshake failed", "err", err)
		_ = client.Close()
		return
	}
	sni := tc.ConnectionState().ServerName

	_ = tc.SetReadDeadline(time.Now().Add(s.cfg.TLS.SniffTimeout.Or(defaultSniffTimeout)))
	prefix, host, err := httpsniff.ReadHeaderBlock(tc, s.cfg.TLS.SniffMaxBytes)
	_ = tc.SetReadDeadline(time.Time{})

	routeHost := host
	if routeHost == "" {
		routeHost = httpsniff.NormalizeHost(sni)
	}
	if err != nil || routeHost == "" {
		log.Warn("tls host sniff failed", "sni", sni, "err", err)
		writeQuickTLS(tc, 400)
		_ = tc.Close()
		return
	}

	msg := control.Msg{
		V: control.Version, Type: control.TypeResolve, ID: control.NewID(),
		Kind: control.KindHTTP, Host: routeHost, SNI: sni, ClientIP: remoteIP,
	}
	// The proxy owns the auth policy and cannot see this request, so forward the
	// details it needs to apply it. A request whose own headers do not fit is sent
	// without them, which the proxy treats as unauthenticated.
	if req, perr := parseRequest(prefix); perr == nil {
		fillAuthDetails(&msg, req)
	} else {
		log.Debug("tls request parse failed, resolving without auth details", "err", perr)
	}

	rCtx, rCancel := context.WithTimeout(context.Background(), s.cfg.TLS.ResolveTimeout.Or(defaultResolveTimeout))
	rep, err := p.Request(rCtx, msg, nil)
	rCancel()
	if err != nil {
		log.Warn("tls resolve failed", "host", routeHost, "sni", sni, "err", err)
		writeQuickTLS(tc, 502)
		_ = tc.Close()
		return
	}
	// The proxy answered with a response of its own: the login redirect, or a
	// refusal. dpipe writes it verbatim and never learns why.
	if rep.Location != "" || rep.Status != 0 {
		log.Info("tls resolve answered by proxy", "host", routeHost, "status", rep.Status)
		writeAuthTLS(tc, rep.Status, rep.Location, rep.SetCookie)
		_ = tc.Close()
		return
	}
	if !rep.Authorized || rep.Target == "" {
		log.Warn("tls resolve denied", "host", routeHost, "sni", sni)
		writeQuickTLS(tc, 502)
		_ = tc.Close()
		return
	}
	// A console hostname: the proxy validated the token and the handshake, so the
	// terminal runs on this connection rather than being forwarded anywhere.
	if rep.Protocol == control.ProtoConsole {
		s.serveTLSConsole(log, id, rep, tc, routeHost, remoteIP, prefix)
		return
	}

	backend, err := net.DialTimeout("tcp", rep.Target, s.cfg.TLS.DialTimeout.Or(defaultDialTimeout))
	if err != nil {
		log.Warn("tls backend dial failed", "target", rep.Target, "err", err)
		writeQuickTLS(tc, 502)
		_ = tc.Close()
		return
	}
	// Replay the decrypted prefix (header block plus any body bytes read with it).
	if _, err := backend.Write(prefix); err != nil {
		log.Warn("tls prefix replay failed", "target", rep.Target, "err", err)
		_ = tc.Close()
		_ = backend.Close()
		return
	}

	log.Info("tls session start", "host", routeHost, "sni", sni, "target", rep.Target,
		"version", tlsVersionName(tc.ConnectionState().Version), "alpn", tc.ConnectionState().NegotiatedProtocol)
	s.reg.Add(id)
	defer s.reg.Done(id)
	Pipe(tc, backend)
	log.Info("tls session end")
}

// certForSNI selects a certificate by exact SNI match, falling back to the
// configured default. A miss fails the handshake cleanly.
func (s *Server) certForSNI(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if c, ok := s.mat.certs[normalizeSNI(chi.ServerName)]; ok {
		return c, nil
	}
	if s.mat.defaultCrt != nil {
		return s.mat.defaultCrt, nil
	}
	return nil, fmt.Errorf("no certificate for sni %q", chi.ServerName)
}

func normalizeSNI(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}

// serveTLSConsole runs a browser terminal on a connection whose TLS this process
// has already terminated, which is why it is served here instead of being handed
// back as a console_accept: a live TLS session cannot be fd-passed. Everything
// about who this is and which guest they may reach was decided by the proxy; what
// is decided here is whether there is room to run it.
func (s *Server) serveTLSConsole(log *slog.Logger, id string, rep control.Msg, tc net.Conn, host, clientIP string, prefix []byte) {
	if !s.cfg.Console.Enabled {
		log.Warn("console over tls but console is not enabled", "host", host)
		writeQuickTLS(tc, 404)
		_ = tc.Close()
		return
	}
	if !s.acquireConsole(host) {
		log.Info("console refused: too many sessions for this vm", "host", host, "sub", rep.Sub)
		writeQuickTLS(tc, 503)
		_ = tc.Close()
		return
	}
	// Anything the client pipelined behind the upgrade request belongs to the
	// websocket stream. It never leaves this process, so MaxConsolePrefix — which
	// bounds what a console_accept can carry — does not apply.
	s.serveConsole(id, control.Msg{
		Host: host, Target: rep.Target, RemoteUser: rep.RemoteUser,
		Sub: rep.Sub, WSKey: rep.WSKey, ClientIP: clientIP,
	}, tc, pipelinedBytes(prefix))
}

// pipelinedBytes returns the bytes of a sniffed prefix that follow the header
// block.
func pipelinedBytes(prefix []byte) []byte {
	if i := bytes.Index(prefix, []byte("\r\n\r\n")); i >= 0 {
		return prefix[i+4:]
	}
	return nil
}

// parseRequest recovers the request line and headers from the decrypted prefix.
// The body is irrelevant here and is left in the prefix for replay.
func parseRequest(prefix []byte) (*http.Request, error) {
	return http.ReadRequest(bufio.NewReader(bytes.NewReader(prefix)))
}

// fillAuthDetails copies the request details the proxy's policy reads into an
// http resolve, within a budget that keeps the message under MaxMsgSize. The
// small headers go first and the cookie takes what is left, because it is the
// only one big enough to crowd the others out — and dpipe cannot tell a session
// cookie from any other, so it must forward the header whole or not at all. A
// field that does not fit is left out, which fails closed: at worst the client
// makes another trip through the login flow.
func fillAuthDetails(m *control.Msg, req *http.Request) {
	budget := control.MaxResolveDetails

	take := func(v string, max int) string {
		if len(v) > max || len(v) > budget {
			return ""
		}
		budget -= len(v)
		return v
	}

	m.Upgrade = take(req.Header.Get("Upgrade"), control.MaxResolveHeader)
	m.Connection = take(req.Header.Get("Connection"), control.MaxResolveHeader)
	m.WSVersion = take(req.Header.Get("Sec-WebSocket-Version"), control.MaxResolveHeader)
	m.WSKey = take(req.Header.Get("Sec-WebSocket-Key"), control.MaxResolveHeader)
	m.Accept = take(req.Header.Get("Accept"), control.MaxResolveHeader)
	m.Path = take(req.URL.RequestURI(), control.MaxResolvePath)
	m.Cookie = take(strings.Join(req.Header.Values("Cookie"), "; "), budget)
}

// writeAuthTLS writes the response the proxy decided on. A location makes it the
// 302 that starts (or finishes) the login round trip; otherwise the status is
// written on its own.
func writeAuthTLS(w io.Writer, status int, location, setCookie string) {
	if location == "" {
		if status == 0 {
			status = 502
		}
		writeQuickTLS(w, status)
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "HTTP/1.1 302 Found\r\nLocation: %s\r\n", location)
	if setCookie != "" {
		fmt.Fprintf(&b, "Set-Cookie: %s\r\n", setCookie)
	}
	b.WriteString("Content-Length: 0\r\nConnection: close\r\n\r\n")
	_, _ = io.WriteString(w, b.String())
}

// writeQuickTLS writes a minimal HTTP/1.1 response over an already-handshaked
// TLS connection.
func writeQuickTLS(w io.Writer, code int) {
	_, _ = io.WriteString(w, quickResponse(code))
}

func quickResponse(code int) string {
	reason := "Bad Request"
	switch code {
	case 400:
		reason = "Bad Request"
	case 401:
		reason = "Unauthorized"
	case 404:
		reason = "Not Found"
	case 502:
		reason = "Bad Gateway"
	case 503:
		reason = "Service Unavailable"
	}
	body := fmt.Sprintf("%d %s\n", code, reason)
	return fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code, reason, len(body), body)
}

// addrString renders an address defensively: adopted sockets (a socketpair, for
// instance) can have an empty or absent peer address.
func addrString(a net.Addr) string {
	if a == nil {
		return ""
	}
	return a.String()
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

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS12:
		return "1.2"
	case tls.VersionTLS13:
		return "1.3"
	}
	return "unknown"
}
