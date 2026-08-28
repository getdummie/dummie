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

func (s *Server) serveTLS(p *control.Peer, id string, client net.Conn) {
	remoteIP := hostOnly(client.RemoteAddr())
	log := s.log.With("id", id, "protocol", control.ProtoTLS, "client", remoteIP)

	cfg := &tls.Config{
		MinVersion: s.mat.tlsMin,
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
	s.serveConsole(id, control.Msg{
		Host: host, Target: rep.Target, RemoteUser: rep.RemoteUser,
		Sub: rep.Sub, WSKey: rep.WSKey, ClientIP: clientIP,
	}, tc, pipelinedBytes(prefix))
}

func pipelinedBytes(prefix []byte) []byte {
	if i := bytes.Index(prefix, []byte("\r\n\r\n")); i >= 0 {
		return prefix[i+4:]
	}
	return nil
}

func parseRequest(prefix []byte) (*http.Request, error) {
	return http.ReadRequest(bufio.NewReader(bytes.NewReader(prefix)))
}

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
