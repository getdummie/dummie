// Package httpproxy fronts upstreams users configure themselves, each at
// <proxy>-<name>.<label>.<tld>. The broker answers with the target, the
// methods it allows, the headers to add and whether to verify its certificate.
package httpproxy

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"path"
	"strings"
	"syscall"
	"time"

	"intproxy/internal/credential"
	"intproxy/internal/integration"
)

func init() {
	integration.Register("http", New)
}

const kindHTTP = "http"

type options struct {
	// Deny holds prefixes no proxy may dial, on top of the loopback,
	// link-local and own addresses that are always refused.
	Deny []string `yaml:"deny"`
}

type HTTPProxy struct {
	name     string
	secure   http.RoundTripper
	insecure http.RoundTripper
}

func New(o integration.Options) (integration.Integration, error) {
	if o.Name == "" {
		return nil, errors.New("http: a name is required")
	}
	if o.Transport == nil {
		return nil, errors.New("http: a transport is required")
	}
	var opts options
	if o.Decode != nil {
		if err := o.Decode(&opts); err != nil {
			return nil, fmt.Errorf("http: %w", err)
		}
	}
	g := guard{}
	for _, raw := range opts.Deny {
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("http: deny %q: %w", raw, err)
		}
		g.deny = append(g.deny, p.Masked())
	}

	secure := guarded(o.Transport, o.DialTimeout, g)
	insecure := guarded(o.Transport, o.DialTimeout, g)
	insecure.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	return &HTTPProxy{name: o.Name, secure: secure, insecure: insecure}, nil
}

func guarded(base *http.Transport, timeout time.Duration, g guard) *http.Transport {
	t := base.Clone()
	t.DialContext = (&net.Dialer{Timeout: timeout, Control: g.control}).DialContext
	return t
}

func (p *HTTPProxy) Name() string { return p.name }

func (p *HTTPProxy) Family() {}

func (p *HTTPProxy) Classify(r *http.Request) (integration.Route, bool) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	label, _, _ := strings.Cut(strings.ToLower(host), ".")
	proxy, ok := strings.CutSuffix(label, "-"+p.name)
	if !ok || proxy == "" {
		return integration.Route{}, false
	}

	// Cleaned so a target's base path stays the floor of what can be reached.
	up := path.Clean("/" + r.URL.Path)
	if strings.HasSuffix(r.URL.Path, "/") && up != "/" {
		up += "/"
	}
	return integration.Route{
		Kind:         kindHTTP,
		Resource:     proxy,
		Write:        r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions,
		UpstreamPath: up,
		Generic:      true,
	}, true
}

func (p *HTTPProxy) Apply(pr *httputil.ProxyRequest, _ integration.Route, tok credential.Token) {
	for k, v := range tok.Headers {
		pr.Out.Header.Set(k, v)
	}
}

func (p *HTTPProxy) RoundTripper(tok credential.Token) http.RoundTripper {
	if tok.InsecureTLS {
		return p.insecure
	}
	return p.secure
}

func (p *HTTPProxy) ModifyResponse(*http.Response) error { return nil }

func (p *HTTPProxy) RenderError(_ integration.Route, _ int, msg string) (string, []byte) {
	return "text/plain; charset=utf-8", []byte(msg + "\n")
}

func (p *HTTPProxy) Describe() string {
	return "intproxy: this host serves a user-configured http proxy"
}

// guard runs on the address actually dialed, after resolution, so a name that
// later resolves somewhere else cannot slip past it.
type guard struct{ deny []netip.Prefix }

var errRefused = errors.New("intproxy: that upstream address is not reachable through a user proxy")

func (g guard) control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return errRefused
	}
	ip := ap.Addr().Unmap()
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return errRefused
	}
	for _, p := range g.deny {
		if p.Contains(ip) {
			return errRefused
		}
	}
	if own(ip) {
		return errRefused
	}
	return nil
}

// own is asked per dial, not cached, since the host's addresses change.
func own(ip netip.Addr) bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return true
	}
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if x, ok := netip.AddrFromSlice(n.IP); ok && x.Unmap() == ip {
			return true
		}
	}
	return false
}
