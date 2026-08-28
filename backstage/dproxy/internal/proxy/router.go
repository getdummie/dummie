package proxy

import (
	"dproxy/internal/httpsniff"
)

type Router struct {
	hosts   map[string]string
	entries map[string]HTTPHost
	def     string
	tcp     map[string]TCPRoute
	tcpList []TCPRoute
}

func NewRouter(cfg *Config) *Router {
	r := &Router{hosts: map[string]string{}, entries: map[string]HTTPHost{}, tcp: map[string]TCPRoute{}}
	if cfg.HTTP != nil {
		for host, h := range cfg.HTTP.Hosts {
			r.hosts[httpsniff.NormalizeHost(host)] = h.Target()
			r.entries[httpsniff.NormalizeHost(host)] = h
		}
		r.def = cfg.HTTP.Default
	}
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

func (r *Router) HostEntry(host string) (HTTPHost, bool) {
	h, ok := r.entries[httpsniff.NormalizeHost(host)]
	return h, ok
}

func (r *Router) TCPRoutes() []TCPRoute { return r.tcpList }
