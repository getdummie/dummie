// Package integration is the seam between the proxy and whatever it fronts.
// The core handles binding, tls, identity and credentials; everything that is
// specific to one upstream lives behind Integration.
package integration

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"sort"
	"strings"

	"intproxy/internal/credential"
)

// Route is what an integration makes of an inbound request: where it goes and
// what credential it needs to get there.
type Route struct {
	// Kind labels the request for logging and for picking an error shape.
	Kind string
	// Resource is what the credential can be scoped to, a repository slug for
	// github. Empty when the request names none.
	Resource string
	Write    bool

	UpstreamHost string
	UpstreamPath string
}

type Integration interface {
	// Name is also the subdomain: <name>.<label>.<tld>.
	Name() string

	// Classify maps an inbound request onto an upstream one, or reports that
	// it is not something this integration serves.
	Classify(*http.Request) (Route, bool)

	// Apply puts the credential on the outbound request.
	Apply(*httputil.ProxyRequest, Route, credential.Token)

	// ModifyResponse fixes up what comes back, chiefly any upstream URL the
	// client is expected to follow.
	ModifyResponse(*http.Response) error

	// RenderError returns a refusal in whatever shape this route's client
	// actually surfaces to the person reading it.
	RenderError(r Route, status int, msg string) (contentType string, body []byte)

	// Describe is the body of a 404, telling a caller what this host serves.
	Describe() string
}

type Options struct {
	// Decode hands the integration its own config block. Nil when the block
	// carried nothing beyond a name.
	Decode func(any) error

	DocsURL string
	// SelfURL is how a client reaches this integration through the proxy.
	SelfURL func() string
	Version string
}

type Factory func(Options) (Integration, error)

var registry = map[string]Factory{}

func Register(name string, f Factory) {
	if _, dup := registry[name]; dup {
		panic("integration: " + name + " is registered twice")
	}
	registry[name] = f
}

func Known(name string) bool {
	_, ok := registry[name]
	return ok
}

func Names() []string {
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func New(name string, o Options) (Integration, error) {
	f, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("integration %q is not known; this build has %s", name, strings.Join(Names(), ", "))
	}
	return f(o)
}
