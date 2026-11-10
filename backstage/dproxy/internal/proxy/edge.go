package proxy

import (
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"dproxy/internal/control"
	"dproxy/internal/httpsniff"
	"dproxy/internal/tlssniff"
)

type edgeRouter struct {
	hosts    map[string]string
	fallback map[string][]string
}

func newEdgeRouter(cfg *EdgeConfig) *edgeRouter {
	r := &edgeRouter{hosts: map[string]string{}, fallback: map[string][]string{}}
	for host, addr := range cfg.Hosts {
		r.hosts[httpsniff.NormalizeHost(host)] = addr
	}
	for domain, addrs := range cfg.Fallback {
		r.fallback[httpsniff.NormalizeHost(domain)] = addrs
	}
	return r
}

// backends also resolves name--<port>.rest and name.<label>.rest to name.rest,
// which is how port hosts and the console and desktop hostnames are spelled.
func (r *edgeRouter) backends(host string) []string {
	h := httpsniff.NormalizeHost(host)
	if h == "" {
		return nil
	}
	if addr, ok := r.hosts[h]; ok {
		return []string{addr}
	}
	if base, _, ok := splitPortHost(h); ok {
		if addr, ok := r.hosts[base]; ok {
			return []string{addr}
		}
	}
	if labels := strings.Split(h, "."); len(labels) >= 3 {
		if addr, ok := r.hosts[strings.Join(append([]string{labels[0]}, labels[2:]...), ".")]; ok {
			return []string{addr}
		}
	}
	for d := h; d != ""; {
		if addrs, ok := r.fallback[d]; ok {
			return addrs
		}
		_, rest, ok := strings.Cut(d, ".")
		if !ok {
			break
		}
		d = rest
	}
	return nil
}

func (p *Proxy) dialEdge(log *slog.Logger, addrs []string, port int) (net.Conn, string) {
	for _, addr := range addrs {
		target := net.JoinHostPort(addr, strconv.Itoa(port))
		conn, err := net.DialTimeout("tcp", target, p.cfg.DialTimeout.Or(defaultDialTimeout))
		if err == nil {
			return conn, target
		}
		log.Warn("edge dial failed", "target", target, "err", err)
	}
	return nil, ""
}

func (p *Proxy) handleEdgeHTTP(client net.Conn) {
	id := control.NewID()
	log := p.log.With("id", id, "protocol", control.ProtoHTTP, "client", client.RemoteAddr().String(), "edge", true)

	_ = client.SetReadDeadline(time.Now().Add(p.cfg.HTTPSniffTimeout.Or(defaultSniffTimeout)))
	prefix, host, err := httpsniff.ReadHeaderBlock(client, p.cfg.HTTPSniffMaxBytes)
	_ = client.SetReadDeadline(time.Time{})
	if err != nil {
		log.Warn("http sniff failed", "err", err)
		writeQuick(client, 400)
		_ = client.Close()
		return
	}

	backend, target := p.dialEdge(log, p.edge.backends(host), p.cfg.Edge.httpPort())
	if backend == nil {
		log.Info("edge deny: no host serves this name", "host", host)
		writeQuick(client, 502)
		_ = client.Close()
		return
	}
	if _, err := backend.Write(prefix); err != nil {
		log.Warn("http prefix replay failed", "target", target, "err", err)
		_ = client.Close()
		_ = backend.Close()
		return
	}
	log.Info("edge route", "host", host, "target", target)
	p.handoffCopy(id, client, backend, control.ProtoHTTP)
}

func (p *Proxy) handleEdgeHTTPS(client net.Conn) {
	id := control.NewID()
	log := p.log.With("id", id, "protocol", control.ProtoTLS, "client", client.RemoteAddr().String(), "edge", true)

	_ = client.SetReadDeadline(time.Now().Add(p.cfg.HTTPSniffTimeout.Or(defaultSniffTimeout)))
	prefix, sni, err := tlssniff.ReadClientHello(client, tlssniff.DefaultMaxBytes)
	_ = client.SetReadDeadline(time.Time{})
	if err != nil {
		log.Warn("client hello sniff failed", "err", err)
		_ = client.Close()
		return
	}

	backend, target := p.dialEdge(log, p.edge.backends(sni), p.cfg.Edge.httpsPort())
	if backend == nil {
		log.Info("edge deny: no host serves this name", "sni", sni)
		_ = client.Close()
		return
	}
	if _, err := backend.Write(prefix); err != nil {
		log.Warn("client hello replay failed", "target", target, "err", err)
		_ = client.Close()
		_ = backend.Close()
		return
	}
	log.Info("edge route", "sni", sni, "target", target)
	p.handoffCopy(id, client, backend, control.ProtoTCP)
}
