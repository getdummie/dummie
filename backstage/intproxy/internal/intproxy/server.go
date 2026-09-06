package intproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type Server struct {
	cfg     *Config
	log     *slog.Logger
	version string

	cert   atomic.Pointer[tls.Certificate]
	broker *broker
	rp     *reverseProxy
}

func New(cfg *Config, log *slog.Logger, version string) (*Server, error) {
	s := &Server{cfg: cfg, log: log, version: version, broker: newBroker(cfg)}
	if err := s.loadCertificate(); err != nil {
		return nil, err
	}
	s.rp = newReverseProxy(s)
	return s, nil
}

// ServerName is the only SNI and Host this proxy answers for. Future
// integrations become sibling names under the same label.
func (s *Server) ServerName() string {
	return "github." + s.cfg.label() + "." + s.cfg.TLD
}

func (s *Server) loadCertificate() error {
	pair, err := tls.LoadX509KeyPair(s.cfg.TLS.Cert, s.cfg.TLS.Key)
	if err != nil {
		return fmt.Errorf("load certificate: %w", err)
	}
	s.cert.Store(&pair)
	return nil
}

func (s *Server) Run(ctx context.Context) error {
	ln, err := s.listen(ctx)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Handler:           s,
		TLSConfig:         s.tlsConfig(),
		ReadHeaderTimeout: s.cfg.ReadHeaderTimeout.Or(30 * time.Second),
		IdleTimeout:       s.cfg.IdleTimeout.Or(120 * time.Second),
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	// No ReadTimeout or WriteTimeout on purpose: a clone of a large repository
	// is one long request in each direction, and any deadline would cut it off.

	go s.watchSIGHUP(ctx)

	go func() {
		<-ctx.Done()
		grace, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownGrace.Or(5*time.Minute))
		defer cancel()
		_ = srv.Shutdown(grace)
	}()

	if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// watchSIGHUP reloads the certificate in place so a renewal does not have to
// restart the process and kill in-flight clones.
func (s *Server) watchSIGHUP(ctx context.Context) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	defer signal.Stop(ch)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			if err := s.loadCertificate(); err != nil {
				s.log.Error("could not reload the certificate", "error", err)
				continue
			}
			s.log.Info("reloaded the certificate")
		}
	}
}

func (s *Server) listen(ctx context.Context) (net.Listener, error) {
	lc := net.ListenConfig{Control: s.control}
	ln, err := lc.Listen(ctx, "tcp4", s.cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("could not listen on %s: %w", s.cfg.Listen, err)
	}
	return ln, nil
}

// control sets the three options that make this bind possible. IP_FREEBIND so
// the address need not exist yet; SO_REUSEADDR and SO_REUSEPORT so a specific
// address can bind alongside dproxy's 0.0.0.0:443, which sets both.
func (s *Server) control(_, _ string, c syscall.RawConn) error {
	var serr error
	cerr := c.Control(func(fd uintptr) {
		if s.cfg.Freebind {
			if e := unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_FREEBIND, 1); e != nil {
				serr = e
				return
			}
		}
		if e := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); e != nil {
			serr = e
			return
		}
		if s.cfg.Reuseport {
			if e := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1); e != nil {
				serr = e
			}
		}
	})
	if cerr != nil {
		return cerr
	}
	return serr
}

func (s *Server) tlsConfig() *tls.Config {
	min := uint16(tls.VersionTLS12)
	if s.cfg.TLS.MinVersion == "1.3" {
		min = tls.VersionTLS13
	}
	return &tls.Config{
		MinVersion: min,
		// http/1.1 only: git and gh both speak it, and h2 buys nothing here
		// while complicating streaming and trailers.
		NextProtos:     []string{"http/1.1"},
		GetCertificate: s.getCertificate,
	}
}

func (s *Server) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if name := strings.ToLower(hello.ServerName); name != "" && name != s.ServerName() {
		return nil, fmt.Errorf("intproxy: %s is not served here", name)
	}
	cert := s.cert.Load()
	if cert == nil {
		return nil, errors.New("intproxy: no certificate is loaded")
	}
	return cert, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if hostOnly(r.Host) != s.ServerName() {
		s.notFound(w)
		return
	}

	req := classify(s.cfg, r)
	if req.kind == kindUnknown {
		s.notFound(w)
		return
	}

	vmIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		s.writeDenial(w, req.kind, http.StatusForbidden, "intproxy: could not determine the calling vm")
		return
	}

	token, err := s.broker.token(r.Context(), vmIP, req)
	if err != nil {
		var d *denial
		if errors.As(err, &d) {
			s.log.Info("refused", "vm_ip", vmIP, "kind", req.kind.String(), "repo", req.repoSlug(),
				"write", req.write, "status", d.status)
			s.writeDenial(w, req.kind, d.status, d.message)
			return
		}
		s.writeDenial(w, req.kind, http.StatusBadGateway, "intproxy: could not obtain a credential for this request")
		return
	}

	s.log.Debug("proxying", "vm_ip", vmIP, "kind", req.kind.String(), "repo", req.repoSlug(), "write", req.write)
	s.rp.serve(w, r, req, token)
}

func hostOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return strings.ToLower(h)
	}
	return strings.ToLower(host)
}
