package dpipe

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
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

	rCtx, rCancel := context.WithTimeout(context.Background(), s.cfg.TLS.ResolveTimeout.Or(defaultResolveTimeout))
	rep, err := p.Request(rCtx, control.Msg{
		V: control.Version, Type: control.TypeResolve, ID: control.NewID(),
		Kind: control.KindHTTP, Host: routeHost, SNI: sni, ClientIP: remoteIP,
	}, nil)
	rCancel()
	if err != nil || !rep.Authorized || rep.Target == "" {
		log.Warn("tls resolve denied", "host", routeHost, "sni", sni, "err", err)
		writeQuickTLS(tc, 502)
		_ = tc.Close()
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
