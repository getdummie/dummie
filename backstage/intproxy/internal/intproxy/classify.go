package intproxy

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

type kind int

const (
	kindUnknown kind = iota
	kindGit
	kindREST
	kindGraphQL
)

func (k kind) String() string {
	switch k {
	case kindGit:
		return "git"
	case kindREST:
		return "rest"
	case kindGraphQL:
		return "graphql"
	}
	return "unknown"
}

type request struct {
	kind  kind
	owner string
	repo  string
	write bool

	upstreamHost string
	upstreamPath string
}

func (r request) repoSlug() string {
	if r.owner == "" || r.repo == "" {
		return ""
	}
	return r.owner + "/" + r.repo
}

// namePattern is the traversal guard: an owner or repo that does not match is
// rejected outright rather than cleaned.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

var (
	gitRefsPattern = regexp.MustCompile(`^/([^/]+)/([^/]+?)(?:\.git)?/info/refs$`)
	gitPackPattern = regexp.MustCompile(`^/([^/]+)/([^/]+?)(?:\.git)?/git-(upload|receive)-pack$`)
	restRepoPrefix = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)(?:/|$)`)
)

// classify maps an inbound path onto an upstream request. gh treats any host
// that is not github.com as GHES, which is why REST arrives under /api/v3.
func classify(cfg *Config, r *http.Request) request {
	path := r.URL.Path

	if path == "/api/graphql" || path == "/api/v3/graphql" {
		return request{
			kind:         kindGraphQL,
			write:        true,
			upstreamHost: cfg.GitHub.apiHost(),
			upstreamPath: "/graphql",
		}
	}

	if rest, ok := restPath(path); ok {
		req := request{
			kind:         kindREST,
			write:        r.Method != http.MethodGet && r.Method != http.MethodHead,
			upstreamHost: cfg.GitHub.apiHost(),
			upstreamPath: rest,
		}
		if m := restRepoPrefix.FindStringSubmatch(rest); m != nil && validNames(m[1], m[2]) {
			req.owner, req.repo = m[1], m[2]
		}
		return req
	}

	if m := gitRefsPattern.FindStringSubmatch(path); m != nil && validNames(m[1], m[2]) {
		service := r.URL.Query().Get("service")
		switch service {
		case "git-upload-pack", "git-receive-pack":
		default:
			return request{}
		}
		return gitRequest(cfg, m[1], m[2], "info/refs", service == "git-receive-pack")
	}

	if m := gitPackPattern.FindStringSubmatch(path); m != nil && validNames(m[1], m[2]) {
		return gitRequest(cfg, m[1], m[2], "git-"+m[3]+"-pack", m[3] == "receive")
	}

	return request{}
}

// restPath matches the /api/v3 prefix only on a segment boundary, so
// /api/v3something is not mistaken for the api.
func restPath(path string) (string, bool) {
	if path == "/api/v3" {
		return "/", true
	}
	if rest, ok := strings.CutPrefix(path, "/api/v3/"); ok {
		return "/" + rest, true
	}
	return "", false
}

// gitRequest pins the .git suffix upstream: github.com accepts both forms, and
// the canonical one avoids a redirect hop.
func gitRequest(cfg *Config, owner, repo, suffix string, write bool) request {
	return request{
		kind:         kindGit,
		owner:        owner,
		repo:         repo,
		write:        write,
		upstreamHost: cfg.GitHub.gitHost(),
		upstreamPath: "/" + owner + "/" + repo + ".git/" + suffix,
	}
}

func validNames(owner, repo string) bool {
	return namePattern.MatchString(owner) && namePattern.MatchString(repo)
}

func (s *Server) upstreamURL(req request, raw url.Values) *url.URL {
	u := &url.URL{Scheme: "https", Host: req.upstreamHost, Path: req.upstreamPath}
	if len(raw) > 0 {
		u.RawQuery = raw.Encode()
	}
	return u
}
