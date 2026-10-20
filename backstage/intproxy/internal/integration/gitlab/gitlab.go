// Package gitlab fronts a gitlab instance for git over https. One build serves
// any number of instances, each under its own name; the upstream base comes
// with every token from the broker.
package gitlab

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strings"

	"intproxy/internal/credential"
	"intproxy/internal/integration"
)

func init() { integration.Register("gitlab", New) }

const kindGit = "git"

type GitLab struct {
	name    string
	version string
}

func New(o integration.Options) (integration.Integration, error) {
	if o.Name == "" {
		return nil, fmt.Errorf("gitlab: a name is required")
	}
	return &GitLab{name: o.Name, version: o.Version}, nil
}

func (g *GitLab) Name() string { return g.name }

// segmentPattern is the traversal guard: a path segment that does not match is
// rejected outright rather than cleaned.
var segmentPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,254}$`)

var (
	gitRefsPattern = regexp.MustCompile(`^/(.+?)(?:\.git)?/info/refs$`)
	gitPackPattern = regexp.MustCompile(`^/(.+?)(?:\.git)?/git-(upload|receive)-pack$`)
)

func (g *GitLab) Classify(r *http.Request) (integration.Route, bool) {
	path := r.URL.Path

	if m := gitRefsPattern.FindStringSubmatch(path); m != nil && validProject(m[1]) {
		service := r.URL.Query().Get("service")
		switch service {
		case "git-upload-pack", "git-receive-pack":
		default:
			return integration.Route{}, false
		}
		return gitRoute(m[1], "info/refs", service == "git-receive-pack"), true
	}

	if m := gitPackPattern.FindStringSubmatch(path); m != nil && validProject(m[1]) {
		return gitRoute(m[1], "git-"+m[2]+"-pack", m[2] == "receive"), true
	}

	return integration.Route{}, false
}

// validProject wants a namespace and a project at least, since gitlab nests
// groups to any depth.
func validProject(p string) bool {
	segs := strings.Split(p, "/")
	if len(segs) < 2 {
		return false
	}
	for _, s := range segs {
		if !segmentPattern.MatchString(s) || strings.Contains(s, "..") {
			return false
		}
	}
	return true
}

func gitRoute(project, suffix string, write bool) integration.Route {
	return integration.Route{
		Kind:         kindGit,
		Resource:     project,
		Write:        write,
		UpstreamPath: "/" + project + ".git/" + suffix,
	}
}

func (g *GitLab) Apply(pr *httputil.ProxyRequest, _ integration.Route, tok credential.Token) {
	pr.Out.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("oauth2:"+tok.Value)))
	if pr.Out.Header.Get("User-Agent") == "" {
		pr.Out.Header.Set("User-Agent", "intproxy/"+g.version)
	}
}

func (g *GitLab) ModifyResponse(resp *http.Response) error {
	resp.Header.Del("Set-Cookie")
	return nil
}

// RenderError is plain text because git prints the body of a refusal as is.
func (g *GitLab) RenderError(_ integration.Route, _ int, msg string) (string, []byte) {
	return "text/plain; charset=utf-8", []byte(msg + "\n")
}

func (g *GitLab) Describe() string {
	return "intproxy: this host serves git over https for gitlab, as /<group>/<project>.git"
}
