package proxy

import (
	"dproxy/internal/httpsniff"
)

// Router is the single source of truth for HTTP routing: the plaintext path
// reads it directly, the HTTPS path reads it via resolve{kind:"http"}.
type Router struct {
	hosts   map[string]string
	entries map[string]HTTPHost
	def     string
	tcp     map[string]TCPRoute
	tcpList []TCPRoute
}

// NewRouter builds the routing tables from the configuration.
func NewRouter(cfg *Config) *Router {
	r := &Router{hosts: map[string]string{}, entries: map[string]HTTPHost{}, tcp: map[string]TCPRoute{}}
	if cfg.HTTP != nil {
		for host, h := range cfg.HTTP.Hosts {
			r.hosts[httpsniff.NormalizeHost(host)] = h.Target()
			r.entries[httpsniff.NormalizeHost(host)] = h
		}
		r.def = cfg.HTTP.Default
	}
	// After the VM table, and never over it: a published guest keeps its name
	// even if the control plane ever names it as a site host too.
	if cfg.Site != nil {
		entry, err := cfg.Site.entry()
		if err == nil {
			for _, host := range cfg.Site.Hosts {
				h := httpsniff.NormalizeHost(host)
				if _, taken := r.entries[h]; taken || h == "" {
					continue
				}
				r.hosts[h] = entry.Target()
				r.entries[h] = entry
			}
		}
	}
	for _, route := range cfg.TCP {
		r.tcp[route.Listen] = route
		r.tcpList = append(r.tcpList, route)
	}
	return r
}

// HostBackend resolves an HTTP host (with or without port, any case) to a
// backend. It falls back to http.default when configured; an empty default means
// no route, which the caller turns into a 502.
func (r *Router) HostBackend(host string) (string, bool) {
	h := httpsniff.NormalizeHost(host)
	if h == "" {
		return "", false
	}
	if t, ok := r.hosts[h]; ok {
		return t, true
	}
	if r.def != "" {
		return r.def, true
	}
	return "", false
}

// HostEntry returns the configured entry for a host. The http.default fallback
// has no entry, so a request routed there is never treated as protected.
func (r *Router) HostEntry(host string) (HTTPHost, bool) {
	h, ok := r.entries[httpsniff.NormalizeHost(host)]
	return h, ok
}

// TCPRoutes returns the configured opaque TCP routes.
func (r *Router) TCPRoutes() []TCPRoute { return r.tcpList }
