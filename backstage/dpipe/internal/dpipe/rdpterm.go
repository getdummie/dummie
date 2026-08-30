package dpipe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"dpipe/internal/control"
	"dpipe/internal/credssp"
	"dpipe/internal/rdp"
)

// confirmFlags is what every connection confirm this proxy writes advertises.
// The confirm has to go out before the client leg names the VM, so the backend's
// own flags are never available in time. Advertising both unconditionally is
// safe: a backend that does not use them ignores the extra client data, while
// omitting DynVCGFXProtocolSupported makes the client drop
// RNS_UD_CS_SUPPORT_DYNVC_GFX_PROTOCOL and gnome-remote-desktop then refuses the
// session during licensing.
const confirmFlags = rdp.ExtendedClientDataSupported | rdp.DynVCGFXProtocolSupported

// serveRDP terminates one native RDP connection: X.224, TLS and NLA on the client
// leg, then the same three on the backend leg with the credentials dproxy handed
// back, then a plain splice. Nothing past NLA is parsed.
func (s *Server) serveRDP(p *control.Peer, id string, client net.Conn) {
	remoteIP := hostOnly(client.RemoteAddr())
	log := s.log.With("id", id, "protocol", control.ProtoRDP, "client", remoteIP)

	defer func() { _ = client.Close() }()

	deadline := time.Now().Add(s.cfg.RDP.HandshakeTimeout.Or(defaultRDPHandshakeTimeout))
	_ = client.SetDeadline(deadline)

	req, err := rdp.ReadConnectionRequest(client)
	if err != nil {
		log.Warn("rdp connection request failed", "err", err)
		return
	}
	// A recognised routing token means this is the second half of a handover the
	// guest started.
	if target, ok := s.handovers.take(req.Cookie); ok {
		s.serveRDPHandover(log, id, client, target, req)
		return
	}

	if req.RequestedProtocols&rdp.ProtocolHybrid == 0 {
		// Without NLA there is no credential to check, so the connection could
		// only ever reach a VM unauthenticated. Refuse with the reason the client
		// knows how to display.
		log.Info("rdp deny: client did not offer NLA", "protocols", req.RequestedProtocols)
		_ = rdp.WriteConnectionFailure(client, req.SrcRef, rdp.FailHybridRequiredByServer)
		return
	}
	if err := rdp.WriteConnectionConfirm(client, req.SrcRef, confirmFlags, rdp.ProtocolHybrid); err != nil {
		log.Warn("rdp connection confirm failed", "err", err)
		return
	}

	if s.mat.rdpCrt == nil {
		log.Error("rdp: no certificate loaded for the client leg")
		return
	}
	ctls := tls.Server(client, &tls.Config{
		Certificates: []tls.Certificate{*s.mat.rdpCrt},
		MinVersion:   s.mat.tlsMin,
	})
	if err := ctls.HandshakeContext(context.Background()); err != nil {
		log.Warn("rdp client tls handshake failed", "err", err)
		return
	}
	leaf, err := leafCert(s.mat.rdpCrt)
	if err != nil {
		log.Error("rdp: unusable certificate", "err", err)
		return
	}

	res, err := credssp.ServeNLA(context.Background(), ctls, credssp.ServerConfig{
		Certificate:  leaf,
		ComputerName: s.cfg.RDP.computerName(),
		DomainName:   s.cfg.RDP.domainName(),
		Authorize:    s.rdpAuthorizer(p, log, remoteIP),
	})
	if err != nil {
		if errors.Is(err, credssp.ErrNotAuthorized) {
			log.Info("rdp not authorized", "err", err)
		} else {
			log.Warn("rdp nla failed", "err", err)
		}
		return
	}

	backend, err := s.dialGuestRDP(log, res.Target, res.RemoteUser, res.RemotePassword)
	if err != nil {
		log.Warn("rdp backend connect failed", "target", res.Target, "err", err)
		return
	}
	defer func() { _ = backend.Close() }()

	// Both legs are past NLA; the rest of the connection sequence is opaque.
	_ = client.SetDeadline(time.Time{})
	s.reg.Add(id)
	defer s.reg.Done(id)
	log.Info("rdp session start", "target", res.Target, "remote_user", res.RemoteUser)
	// Counted, because a post-NLA failure and a backend that hangs up at once
	// look identical from here otherwise.
	toGuest, toClient := s.spliceWatchingRedirects(log, id, ctls, backend, res.Target)
	log.Info("rdp session end", "bytes_to_guest", toGuest, "bytes_to_client", toClient)
}

// spliceWatchingRedirects joins the two legs and registers the routing token of
// any Server Redirection the guest sends, so the reconnect that follows can be
// sent back to the same guest.
func (s *Server) spliceWatchingRedirects(log *slog.Logger, id string, client, backend net.Conn, target string) (toGuest, toClient int64) {
	watch := &rdp.RedirectScanner{}
	return pipeCounted(id, client, backend, func(b []byte) {
		if token := watch.Scan(b); token != nil {
			s.handovers.put(token, target)
			log.Info("rdp handover offered", "target", target)
		}
	})
}

// serveRDPHandover answers the reconnect half of a handover. The routing token
// has already named the guest, and the credentials the client replays were
// minted by that guest and are checked by it, so this leg terminates RDSTLS not
// to authorise anything but to keep the stream visible: gnome-remote-desktop
// hands over twice, and a raw splice hides the second redirection.
func (s *Server) serveRDPHandover(log *slog.Logger, id string, client net.Conn, target string, req *rdp.ConnectionRequest) {
	if req.RequestedProtocols&rdp.ProtocolRDSTLS == 0 {
		log.Info("rdp handover without rdstls, splicing", "protocols", req.RequestedProtocols)
		s.spliceRDPHandover(log, id, client, target, req.Raw)
		return
	}
	if s.mat.rdpCrt == nil {
		log.Error("rdp: no certificate loaded for the handover leg")
		return
	}
	if err := rdp.WriteConnectionConfirm(client, req.SrcRef, confirmFlags, rdp.ProtocolRDSTLS); err != nil {
		log.Warn("rdp handover confirm failed", "err", err)
		return
	}
	ctls := tls.Server(client, &tls.Config{
		Certificates: []tls.Certificate{*s.mat.rdpCrt},
		MinVersion:   s.mat.tlsMin,
	})
	if err := ctls.HandshakeContext(context.Background()); err != nil {
		log.Warn("rdp handover tls handshake failed", "err", err)
		return
	}
	if err := rdp.WriteRDSTLSCapabilities(ctls); err != nil {
		log.Warn("rdp handover capabilities failed", "err", err)
		return
	}
	auth, err := rdp.ReadRDSTLSAuthRequest(ctls)
	if err != nil {
		log.Warn("rdp handover authentication request failed", "err", err)
		return
	}
	log.Info("rdp handover authenticating", "target", target, "data_type", auth.DataType,
		"remote_user", rdp.RDSTLSString(auth.UserName), "password_bytes", len(auth.Password))

	backend, code, err := s.dialGuestRDSTLS(log, target, req.Cookie, auth)
	if err != nil {
		log.Warn("rdp handover backend connect failed", "target", target, "err", err)
		if code == rdp.RDSTLSSuccess {
			code = rdp.RDSTLSAccessDenied
		}
		_ = rdp.WriteRDSTLSAuthResponse(ctls, code)
		return
	}
	defer func() { _ = backend.Close() }()

	if err := rdp.WriteRDSTLSAuthResponse(ctls, rdp.RDSTLSSuccess); err != nil {
		log.Warn("rdp handover authentication response failed", "err", err)
		return
	}

	_ = client.SetDeadline(time.Time{})
	s.reg.Add(id)
	defer s.reg.Done(id)
	log.Info("rdp handover start", "target", target)
	toGuest, toClient := s.spliceWatchingRedirects(log, id, ctls, backend, target)
	log.Info("rdp handover end", "bytes_to_guest", toGuest, "bytes_to_client", toClient)
}

// spliceRDPHandover hands a reconnect this proxy cannot terminate straight to
// the guest, starting with the connection request already read off the wire.
func (s *Server) spliceRDPHandover(log *slog.Logger, id string, client net.Conn, target string, request []byte) {
	backend, err := net.DialTimeout("tcp", target, s.cfg.RDP.DialTimeout.Or(defaultDialTimeout))
	if err != nil {
		log.Warn("rdp handover connect failed", "target", target, "err", err)
		return
	}
	defer func() { _ = backend.Close() }()

	if _, err := backend.Write(request); err != nil {
		log.Warn("rdp handover replay failed", "target", target, "err", err)
		return
	}

	_ = client.SetDeadline(time.Time{})
	s.reg.Add(id)
	defer s.reg.Done(id)
	log.Info("rdp handover start", "target", target)
	toGuest, toClient := pipeCounted(id, client, backend, nil)
	log.Info("rdp handover end", "bytes_to_guest", toGuest, "bytes_to_client", toClient)
}

// leafCert returns the parsed leaf, parsing it on demand for certificates loaded
// without one.
func leafCert(c *tls.Certificate) (*x509.Certificate, error) {
	if c.Leaf != nil {
		return c.Leaf, nil
	}
	if len(c.Certificate) == 0 {
		return nil, errors.New("certificate holds no DER")
	}
	return x509.ParseCertificate(c.Certificate[0])
}

// rdpAuthorizer forwards the client's NTLM material to dproxy, which holds the NT
// hash. dpipe never sees the RDP password.
func (s *Server) rdpAuthorizer(p *control.Peer, log *slog.Logger, clientIP string) credssp.Authorizer {
	return func(ctx context.Context, req credssp.AuthRequest) (credssp.AuthResult, error) {
		var zero credssp.AuthResult
		if len(req.NTResponse) > control.MaxNTLMBlob {
			return zero, fmt.Errorf("%w: nt response too large", credssp.ErrNotAuthorized)
		}

		ctx, cancel := context.WithTimeout(ctx, s.cfg.RDP.ResolveTimeout.Or(defaultResolveTimeout))
		defer cancel()

		rep, err := p.Request(ctx, control.Msg{
			V: control.Version, Type: control.TypeResolve, ID: control.NewID(),
			Kind:            control.KindRDP,
			RDPUser:         req.User,
			RDPDomain:       req.Domain,
			RDPWorkstation:  req.Workstation,
			RDPChallenge:    base64.StdEncoding.EncodeToString(req.ServerChallenge[:]),
			RDPNTResponse:   base64.StdEncoding.EncodeToString(req.NTResponse),
			ClientIP:        clientIP,
		}, nil)
		if err != nil {
			log.Warn("rdp resolve failed", "user", req.User, "err", err)
			return zero, errors.New("authorization unavailable")
		}
		if !rep.Authorized || rep.Target == "" {
			log.Info("rdp credentials refused", "user", req.User)
			return zero, credssp.ErrNotAuthorized
		}
		key, err := base64.StdEncoding.DecodeString(rep.RDPSessionKey)
		if err != nil || len(key) != 16 {
			log.Warn("rdp resolve returned an unusable session key", "user", req.User)
			return zero, errors.New("authorization unavailable")
		}
		// Logged in full because this is the one NLA exchange known to work: it
		// is the reference for what the backend leg should be sending.
		log.Info("rdp authorized", "user", req.User, "domain", req.Domain,
			"workstation", req.Workstation, "client_flags", req.Flags,
			"nt_response_bytes", len(req.NTResponse), "has_mic", req.HasMIC,
			"target", rep.Target, "remote_user", rep.RemoteUser)
		return credssp.AuthResult{
			SessionBaseKey: key,
			Target:         rep.Target,
			RemoteUser:     rep.RemoteUser,
			RemotePassword: rep.RemotePassword,
		}, nil
	}
}

// dialGuestRDP brings up the backend leg: TCP, X.224 with NLA requested, TLS, then
// CredSSP as a client. It is shared with the browser desktop path.
func (s *Server) dialGuestRDP(log *slog.Logger, target, user, password string) (net.Conn, error) {
	dialTimeout := s.cfg.RDP.DialTimeout.Or(defaultDialTimeout)
	nc, err := net.DialTimeout("tcp", target, dialTimeout)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = nc.Close()
		}
	}()
	_ = nc.SetDeadline(time.Now().Add(s.cfg.RDP.HandshakeTimeout.Or(defaultRDPHandshakeTimeout)))

	if err := rdp.WriteConnectionRequest(nc, "", rdp.ProtocolHybrid|rdp.ProtocolSSL); err != nil {
		return nil, fmt.Errorf("connection request: %w", err)
	}
	selected, err := rdp.ReadConnectionConfirm(nc)
	if err != nil {
		return nil, err
	}
	if selected != rdp.ProtocolHybrid {
		return nil, fmt.Errorf("backend selected protocol %d, want NLA", selected)
	}

	// The guest presents a self-signed certificate generated at image build time,
	// so chain verification cannot apply. CredSSP binds the session to this exact
	// key, which is what actually detects a substituted backend.
	btls := tls.Client(nc, &tls.Config{InsecureSkipVerify: true})
	if err := btls.HandshakeContext(context.Background()); err != nil {
		return nil, fmt.Errorf("backend tls handshake: %w", err)
	}
	chain := btls.ConnectionState().PeerCertificates
	if len(chain) == 0 {
		return nil, errors.New("backend presented no certificate")
	}
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		host = target
	}
	if err := credssp.DialNLA(btls, credssp.ClientConfig{
		Certificate: chain[0],
		Log:         log,
		SPN:         "TERMSRV/" + host,
		User:        user,
		Password:    password,
		Workstation: s.cfg.RDP.computerName(),
	}); err != nil {
		return nil, fmt.Errorf("backend nla: %w", err)
	}

	_ = nc.SetDeadline(time.Time{})
	ok = true
	return btls, nil
}

// dialGuestRDSTLS brings up the backend leg of a handover: TCP with the client's
// routing token replayed, X.224 selecting RDSTLS, TLS, then the client's
// authentication request forwarded byte for byte. The second return is the
// guest's result code when it produced one, so a refusal can be passed on.
func (s *Server) dialGuestRDSTLS(log *slog.Logger, target, cookie string, auth *rdp.RDSTLSAuthRequest) (net.Conn, uint32, error) {
	nc, err := net.DialTimeout("tcp", target, s.cfg.RDP.DialTimeout.Or(defaultDialTimeout))
	if err != nil {
		return nil, 0, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = nc.Close()
		}
	}()
	_ = nc.SetDeadline(time.Now().Add(s.cfg.RDP.HandshakeTimeout.Or(defaultRDPHandshakeTimeout)))

	if err := rdp.WriteConnectionRequest(nc, cookie, rdp.ProtocolRDSTLS); err != nil {
		return nil, 0, fmt.Errorf("connection request: %w", err)
	}
	selected, err := rdp.ReadConnectionConfirm(nc)
	if err != nil {
		return nil, 0, err
	}
	if selected != rdp.ProtocolRDSTLS {
		return nil, 0, fmt.Errorf("backend selected protocol %d, want RDSTLS", selected)
	}

	// The guest's certificate is self-signed and the redirection names no target
	// certificate to pin it against. What actually gates this leg is the routing
	// token, and then the credentials the guest minted and verifies below.
	btls := tls.Client(nc, &tls.Config{InsecureSkipVerify: true})
	if err := btls.HandshakeContext(context.Background()); err != nil {
		return nil, 0, fmt.Errorf("backend tls handshake: %w", err)
	}
	if err := rdp.ReadRDSTLSCapabilities(btls); err != nil {
		return nil, 0, fmt.Errorf("backend rdstls capabilities: %w", err)
	}
	if _, err := btls.Write(auth.Raw); err != nil {
		return nil, 0, fmt.Errorf("backend rdstls authentication request: %w", err)
	}
	code, err := rdp.ReadRDSTLSAuthResponse(btls)
	if err != nil {
		return nil, 0, fmt.Errorf("backend rdstls authentication response: %w", err)
	}
	if code != rdp.RDSTLSSuccess {
		return nil, code, fmt.Errorf("backend refused the handover credentials (0x%08x)", code)
	}
	log.Debug("rdp handover backend authenticated", "target", target)

	_ = nc.SetDeadline(time.Time{})
	ok = true
	return btls, rdp.RDSTLSSuccess, nil
}

// rdpDumpDir enables a capture of the post-NLA stream when it exists. Creating
// the directory turns it on and removing it turns it off, so a live host needs
// no config change to be inspected. It writes decrypted RDP, so it is a
// debugging tool only: remove the directory when finished.
const rdpDumpDir = "/etc/dpipe/rdp-dump"

func rdpDump(id, direction string) io.WriteCloser {
	if _, err := os.Stat(rdpDumpDir); err != nil {
		return nil
	}
	f, err := os.Create(filepath.Join(rdpDumpDir, id+"-"+direction+".bin"))
	if err != nil {
		return nil
	}
	return f
}

// pipeCounted is Pipe with byte counters on each direction, an optional capture
// of what crossed, and an optional tap on the server-to-client half.
func pipeCounted(id string, client, backend net.Conn, toClientTap func([]byte)) (toGuest, toClient int64) {
	half := func(dst, src net.Conn, direction string, n *int64) {
		var w io.Writer = dst
		if f := rdpDump(id, direction); f != nil {
			defer f.Close()
			w = io.MultiWriter(dst, f)
		}
		if direction == "to-client" && toClientTap != nil {
			w = io.MultiWriter(w, tapWriter(toClientTap))
		}
		*n, _ = io.Copy(w, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); half(backend, client, "to-guest", &toGuest) }()
	go func() { defer wg.Done(); half(client, backend, "to-client", &toClient) }()
	wg.Wait()
	_ = client.Close()
	_ = backend.Close()
	return toGuest, toClient
}

type tapWriter func([]byte)

func (f tapWriter) Write(p []byte) (int, error) {
	f(p)
	return len(p), nil
}
