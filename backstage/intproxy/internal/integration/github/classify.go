package github

import (
	"net/http"
	"regexp"
	"strings"

	"intproxy/internal/integration"
)

const (
	kindGit     = "git"
	kindREST    = "rest"
	kindGraphQL = "graphql"
)

// namePattern is the traversal guard: an owner or repo that does not match is
// rejected outright rather than cleaned.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

var (
	gitRefsPattern = regexp.MustCompile(`^/([^/]+)/([^/]+?)(?:\.git)?/info/refs$`)
	gitPackPattern = regexp.MustCompile(`^/([^/]+)/([^/]+?)(?:\.git)?/git-(upload|receive)-pack$`)
	restRepoPrefix = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)(?:/|$)`)
)

// Classify maps an inbound path onto an upstream request. gh treats any host
// that is not github.com as GitHub Enterprise Server, which is why REST arrives
// under /api/v3 and GraphQL at /api/graphql.
func (g *GitHub) Classify(r *http.Request) (integration.Route, bool) {
	path := r.URL.Path

	if path == "/api/graphql" || path == "/api/v3/graphql" {
		return integration.Route{
			Kind:         kindGraphQL,
			Write:        true,
			UpstreamHost: g.apiHost,
			UpstreamPath: "/graphql",
		}, true
	}

	if rest, ok := restPath(path); ok {
		route := integration.Route{
			Kind:         kindREST,
			Write:        r.Method != http.MethodGet && r.Method != http.MethodHead,
			UpstreamHost: g.apiHost,
			UpstreamPath: rest,
		}
		if m := restRepoPrefix.FindStringSubmatch(rest); m != nil && validNames(m[1], m[2]) {
			route.Resource = m[1] + "/" + m[2]
		}
		return route, true
	}

	if m := gitRefsPattern.FindStringSubmatch(path); m != nil && validNames(m[1], m[2]) {
		service := r.URL.Query().Get("service")
		switch service {
		case "git-upload-pack", "git-receive-pack":
		default:
			return integration.Route{}, false
		}
		return g.gitRoute(m[1], m[2], "info/refs", service == "git-receive-pack"), true
	}

	if m := gitPackPattern.FindStringSubmatch(path); m != nil && validNames(m[1], m[2]) {
		return g.gitRoute(m[1], m[2], "git-"+m[3]+"-pack", m[3] == "receive"), true
	}

	return integration.Route{}, false
}

// restPath matches the /api/v3 prefix only on a segment boundary, so
// /api/v3something is not mistaken for the api. A bare /repos/… at the root is
// deliberately not accepted: / is the git namespace, and owner=repos would be
// ambiguous.
func restPath(path string) (string, bool) {
	if path == "/api/v3" {
		return "/", true
	}
	if rest, ok := strings.CutPrefix(path, "/api/v3/"); ok {
		return "/" + rest, true
	}
	return "", false
}

// gitRoute pins the .git suffix upstream: github.com accepts both forms, and
// the canonical one avoids a redirect hop.
func (g *GitHub) gitRoute(owner, repo, suffix string, write bool) integration.Route {
	return integration.Route{
		Kind:         kindGit,
		Resource:     owner + "/" + repo,
		Write:        write,
		UpstreamHost: g.gitHost,
		UpstreamPath: "/" + owner + "/" + repo + ".git/" + suffix,
	}
}

func validNames(owner, repo string) bool {
	return namePattern.MatchString(owner) && namePattern.MatchString(repo)
}
