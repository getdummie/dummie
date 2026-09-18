// Package github fronts github.com: git over https, the REST api under
// /api/v3 and GraphQL at /api/graphql, with the credential added on the
// upstream leg so it never reaches the client.
package github

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strings"

	"intproxy/internal/credential"
	"intproxy/internal/integration"
)

func init() { integration.Register("github", New) }

const (
	defaultGitHost = "github.com"
	defaultAPIHost = "api.github.com"
)

type Config struct {
	GitHost string `yaml:"git_host"`
	APIHost string `yaml:"api_host"`
}

type GitHub struct {
	gitHost string
	apiHost string
	self    func() string
	docs    string
	version string
}

func New(o integration.Options) (integration.Integration, error) {
	var c Config
	if o.Decode != nil {
		if err := o.Decode(&c); err != nil {
			return nil, fmt.Errorf("github: %w", err)
		}
	}
	return &GitHub{
		gitHost: or(c.GitHost, defaultGitHost),
		apiHost: or(c.APIHost, defaultAPIHost),
		self:    o.SelfURL,
		docs:    o.DocsURL,
		version: o.Version,
	}, nil
}

func (g *GitHub) Name() string { return "github" }

func (g *GitHub) Apply(pr *httputil.ProxyRequest, r integration.Route, tok credential.Token) {
	switch r.Kind {
	case kindGit:
		// The documented token form for git over https, and the one that needs
		// no credential helper in the client. Works for an installation token
		// and a personal access token alike.
		pr.Out.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("x-access-token:"+tok.Value)))
	default:
		pr.Out.Header.Set("Authorization", "Bearer "+tok.Value)
		if pr.Out.Header.Get("Accept") == "" {
			pr.Out.Header.Set("Accept", "application/vnd.github+json")
		}
		if pr.Out.Header.Get("X-GitHub-Api-Version") == "" {
			pr.Out.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		}
	}

	if pr.Out.Header.Get("User-Agent") == "" {
		pr.Out.Header.Set("User-Agent", "intproxy/"+g.version)
	}
}

var linkURLPattern = regexp.MustCompile(`<([^>]+)>`)

func (g *GitHub) ModifyResponse(resp *http.Response) error {
	resp.Header.Del("Set-Cookie")

	// gh follows Link for pagination. Unrewritten, the client chases
	// api.github.com with no credential and silently truncates at page one.
	if link := resp.Header.Get("Link"); link != "" {
		resp.Header.Set("Link", linkURLPattern.ReplaceAllStringFunc(link, func(m string) string {
			return "<" + g.rewriteURL(m[1:len(m)-1]) + ">"
		}))
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		resp.Header.Set("Location", g.rewriteURL(loc))
	}
	return nil
}

// Response bodies are not rewritten, so fields like clone_url and html_url
// still name github.com. Known limitation.
func (g *GitHub) rewriteURL(u string) string {
	if g.self == nil {
		return u
	}
	self := g.self()
	if rest, ok := strings.CutPrefix(u, "https://"+g.apiHost); ok {
		return self + "/api/v3" + rest
	}
	if rest, ok := strings.CutPrefix(u, "https://"+g.gitHost); ok {
		return self + rest
	}
	return u
}

// RenderError renders one refusal three ways, because git, gh and a gh graphql
// call each surface a different part of the response to the person reading it.
// Never a 401: git reacts to one by prompting for credentials, which is the
// confusing outcome this whole thing exists to avoid.
func (g *GitHub) RenderError(r integration.Route, _ int, msg string) (string, []byte) {
	switch r.Kind {
	case kindREST:
		body, _ := json.Marshal(map[string]string{
			"message":           msg,
			"documentation_url": g.docs,
		})
		return "application/json; charset=utf-8", append(body, '\n')
	case kindGraphQL:
		body, _ := json.Marshal(map[string]any{
			"errors": []map[string]string{{"message": msg}},
		})
		return "application/json; charset=utf-8", append(body, '\n')
	default:
		return "text/plain; charset=utf-8", []byte(msg + "\n")
	}
}

func (g *GitHub) Describe() string {
	return "intproxy: this host serves git over https, the github rest api under /api/v3, and graphql at /api/graphql"
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
