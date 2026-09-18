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
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func (s *Server) Run(ctx context.Context) error {
	ln, err := s.listen(ctx)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: s.cfg.ReadHeaderTimeout.Or(30 * time.Second),
		IdleTimeout:       s.cfg.IdleTimeout.Or(120 * time.Second),
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	if s.cfg.TLS.enabled() {
		srv.TLSConfig = s.tlsConfig()
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

	serve := srv.Serve
	if s.cfg.TLS.enabled() {
		serve = func(l net.Listener) error { return srv.ServeTLS(l, "", "") }
	}
	if err := serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// watchSIGHUP reloads the certificate and the policy in place, so neither a
// renewal nor a rule change needs a restart that would kill in-flight clones.
func (s *Server) watchSIGHUP(ctx context.Context) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	defer signal.Stop(ch)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			if err := s.reload(); err != nil {
				s.log.Error("reload failed, so the running configuration is unchanged", "error", err)
				continue
			}
			s.log.Info("reloaded")
		}
	}
}

func (s *Server) reload() error {
	if s.cfg.TLS.enabled() {
		if err := s.loadCertificate(); err != nil {
			return err
		}
	}
	if s.cfg.Credential.Mode != ModeLocal || s.cfg.path == "" {
		return nil
	}

	fresh, err := LoadConfig(s.cfg.path)
	if err != nil {
		return err
	}
	if fresh.Credential.Mode != ModeLocal {
		return errors.New("the config on disk no longer uses mode: local, which needs a restart")
	}
	if err := fresh.Policy.Compile(); err != nil {
		return err
	}
	s.policy.Store(fresh.Policy)
	return nil
}

func (s *Server) listen(ctx context.Context) (net.Listener, error) {
	lc := net.ListenConfig{Control: s.control}
	ln, err := lc.Listen(ctx, "tcp4", s.cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("could not listen on %s: %w", s.cfg.Listen, err)
	}
	return ln, nil
}

// control sets the options that let this bind share a port. IP_FREEBIND so the
// address need not exist yet; SO_REUSEADDR and SO_REUSEPORT so a specific
// address can bind alongside another process holding the wildcard. The two are
// not a reuseport group -- that needs identical bind addresses -- so there is
// no load balancing between them: the kernel scores the specific match higher
// and every connection to this address arrives here.
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
	if name := strings.ToLower(hello.ServerName); name != "" {
		if _, ok := s.hosts[name]; !ok {
			return nil, fmt.Errorf("intproxy: %s is not served here", name)
		}
	}
	cert := s.cert.Load()
	if cert == nil {
		return nil, errors.New("intproxy: no certificate is loaded")
	}
	return cert, nil
}
