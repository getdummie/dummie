package intproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"intproxy/internal/credential"
	"intproxy/internal/integration"
	"intproxy/internal/policy"
)

type Server struct {
	cfg     *Config
	log     *slog.Logger
	version string

	cert   atomic.Pointer[tls.Certificate]
	policy atomic.Pointer[policy.Policy]

	creds *credential.Cache
	hosts map[string]integration.Integration
	names []string

	rp *httputil.ReverseProxy
}

func New(cfg *Config, log *slog.Logger, version string) (*Server, error) {
	s := &Server{
		cfg:     cfg,
		log:     log,
		version: version,
		hosts:   map[string]integration.Integration{},
	}

	if cfg.TLS.enabled() {
		if err := s.loadCertificate(); err != nil {
			return nil, err
		}
	} else {
		log.Warn("tls is off, so clients reach this proxy over plain http")
	}

	src, err := s.credentialSource()
	if err != nil {
		return nil, err
	}
	s.creds = credential.NewCache(src, cfg.Credential.TokenSkew.Or(time.Minute))

	if cfg.Credential.Mode == ModeLocal {
		if err := cfg.Policy.Compile(); err != nil {
			return nil, err
		}
		s.policy.Store(cfg.Policy)
	}

	for _, ic := range cfg.Integrations {
		if !ic.Enabled {
			continue
		}
		host := cfg.hostname(ic.Name)
		ig, err := integration.New(ic.Name, integration.Options{
			Decode:  ic.Decode,
			DocsURL: cfg.DocsURL,
			SelfURL: func() string { return s.selfURL(host) },
			Version: version,
		})
		if err != nil {
			return nil, err
		}
		s.hosts[host] = ig
		s.names = append(s.names, host)
	}
	sort.Strings(s.names)

	s.rp = s.newReverseProxy()
	return s, nil
}

// Hostnames are every vhost this proxy answers for.
func (s *Server) Hostnames() []string { return s.names }

func (s *Server) credentialSource() (credential.Source, error) {
	switch s.cfg.Credential.Mode {
	case ModeBroker:
		return credential.NewBroker(credential.BrokerConfig{
			Socket:  s.cfg.Credential.Broker.Socket,
			Timeout: s.cfg.Credential.Broker.Timeout.Or(5 * time.Second),
		}, s.cfg.DocsURL), nil

	case ModeLocal:
		sources := multiSource{}
		for _, ic := range s.cfg.Integrations {
			if !ic.Enabled {
				continue
			}
			static, err := credential.NewStatic(ic.Auth)
			if err != nil {
				return nil, fmt.Errorf("integration %q: %w", ic.Name, err)
			}
			sources[ic.Name] = static
			s.log.Warn("this proxy holds the credentials itself, so a compromise exposes them",
				"integration", ic.Name, "credentials", strings.Join(static.Names(), ","))
		}
		return sources, nil
	}
	return nil, fmt.Errorf("credential.mode %q is not known", s.cfg.Credential.Mode)
}

// multiSource routes to the credentials configured for each integration.
type multiSource map[string]credential.Source

func (m multiSource) Token(ctx context.Context, s credential.Scope) (credential.Token, error) {
	src, ok := m[s.Integration]
	if !ok {
		return credential.Token{}, credential.Denyf(http.StatusForbidden,
			"intproxy: no credential is configured for the %s integration", s.Integration)
	}
	return src.Token(ctx, s)
}

func (s *Server) loadCertificate() error {
	pair, err := tls.LoadX509KeyPair(s.cfg.TLS.Cert, s.cfg.TLS.Key)
	if err != nil {
		return fmt.Errorf("load certificate: %w", err)
	}
	s.cert.Store(&pair)
	return nil
}

// selfURL is how a client reaches this proxy, which is what upstream URLs in
// Link and Location headers get rewritten to.
func (s *Server) selfURL(host string) string {
	if s.cfg.TLS.enabled() {
		return "https://" + host
	}
	return "http://" + host
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ig, ok := s.hosts[hostOnly(r.Host)]
	if !ok {
		s.notFound(w)
		return
	}

	route, ok := ig.Classify(r)
	if !ok {
		s.writeError(w, ig, integration.Route{}, http.StatusNotFound, ig.Describe())
		return
	}

	ip, err := clientIP(r.RemoteAddr)
	if err != nil {
		s.writeError(w, ig, route, http.StatusForbidden, "intproxy: could not determine the calling client")
		return
	}

	scope := credential.Scope{
		Integration: ig.Name(),
		ClientIP:    ip.String(),
		Resource:    route.Resource,
		Write:       route.Write,
	}

	if p := s.policy.Load(); p != nil {
		d, err := p.Allow(ip, ig.Name(), route.Resource, route.Write)
		if err != nil {
			s.deny(w, ig, route, ip, err)
			return
		}
		scope.Credential = d.Credential
	}

	tok, err := s.creds.Token(r.Context(), scope)
	if err != nil {
		s.deny(w, ig, route, ip, err)
		return
	}

	s.log.Debug("proxying", "client", ip.String(), "integration", ig.Name(),
		"kind", route.Kind, "resource", route.Resource, "write", route.Write)
	s.proxy(w, r, ig, route, tok)
}

func (s *Server) deny(w http.ResponseWriter, ig integration.Integration, route integration.Route, ip netip.Addr, err error) {
	var d *credential.Denial
	if !errors.As(err, &d) {
		s.log.Warn("could not obtain a credential", "client", ip.String(), "integration", ig.Name(), "error", err)
		s.writeError(w, ig, route, http.StatusBadGateway, "intproxy: could not obtain a credential for this request")
		return
	}
	s.log.Info("refused", "client", ip.String(), "integration", ig.Name(), "kind", route.Kind,
		"resource", route.Resource, "write", route.Write, "status", d.Status, "reason", d.Message)
	s.writeError(w, ig, route, d.Status, d.Message)
}

func (s *Server) writeError(w http.ResponseWriter, ig integration.Integration, route integration.Route, status int, msg string) {
	ct, body := ig.RenderError(route, status, msg)
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (s *Server) notFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = fmt.Fprintf(w, "intproxy: nothing is served on this host; this proxy answers for %s\n", strings.Join(s.names, ", "))
}

func hostOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return strings.ToLower(h)
	}
	return strings.ToLower(host)
}

// clientIP is the whole of this proxy's identity model. In a managed fleet it
// is trustworthy because the firewall drops any packet whose source is not the
// exact address allocated to that interface; standing alone, it is only as
// good as the network it sits on.
func clientIP(remote string) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return netip.Addr{}, err
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, err
	}
	return addr.Unmap(), nil
}
