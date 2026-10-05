package proxy

import (
	"strconv"
	"strings"

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
	if e, ok := r.portEntry(h); ok {
		return e.Target(), true
	}
	if r.def != "" {
		return r.def, true
	}
	return "", false
}

// HostEntry also answers for name--<port>.rest, as the entry for name.rest
// with that port as its default, so routing and auth both see the right port.
func (r *Router) HostEntry(host string) (HTTPHost, bool) {
	h := httpsniff.NormalizeHost(host)
	if e, ok := r.entries[h]; ok {
		return e, true
	}
	return r.portEntry(h)
}

// PublishedEntry is HostEntry without the name--<port> form.
func (r *Router) PublishedEntry(host string) (HTTPHost, bool) {
	h, ok := r.entries[httpsniff.NormalizeHost(host)]
	return h, ok
}

func (r *Router) portEntry(host string) (HTTPHost, bool) {
	base, port, ok := splitPortHost(host)
	if !ok {
		return HTTPHost{}, false
	}
	e, ok := r.entries[base]
	if !ok || !e.PortHosts {
		return HTTPHost{}, false
	}
	e.DefaultPort = port
	return e, true
}

func splitPortHost(host string) (string, int, bool) {
	label, rest, ok := strings.Cut(host, ".")
	if !ok || rest == "" {
		return "", 0, false
	}
	name, digits, ok := strings.Cut(label, portSeparator)
	if !ok || name == "" {
		return "", 0, false
	}
	port, err := strconv.Atoi(digits)
	if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != digits {
		return "", 0, false
	}
	return name + "." + rest, port, true
}

func (r *Router) TCPRoutes() []TCPRoute { return r.tcpList }
