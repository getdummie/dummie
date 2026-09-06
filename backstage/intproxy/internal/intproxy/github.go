package intproxy

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strings"
	"time"
)

type reverseProxy struct {
	s  *Server
	rp *httputil.ReverseProxy
}

type ctxKey int

const (
	ctxRequest ctxKey = iota
	ctxToken
)

// stripped headers carry identity the guest must not be able to smuggle
// upstream, and that we replace with our own credential.
var stripped = []string{
	"Authorization",
	"Cookie",
	"Proxy-Authorization",
	"Forwarded",
	"X-Forwarded-For",
	"X-Forwarded-Host",
	"X-Forwarded-Proto",
	"X-Real-Ip",
}

func newReverseProxy(s *Server) *reverseProxy {
	p := &reverseProxy{s: s}
	p.rp = &httputil.ReverseProxy{
		Rewrite:        p.rewrite,
		ModifyResponse: p.modifyResponse,
		ErrorHandler:   p.upstreamError,
		// Flush every write: git's sideband progress and gh's streaming output
		// both stall behind a buffer.
		FlushInterval: -1,
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: s.cfg.DialTimeout.Or(10 * time.Second)}).DialContext,
			TLSHandshakeTimeout:   s.cfg.TLSHandshakeTimeout.Or(10 * time.Second),
			ResponseHeaderTimeout: s.cfg.ResponseHeaderTimeout.Or(60 * time.Second),
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConnsPerHost:   16,
			ForceAttemptHTTP2:     true,
			DisableCompression:    true,
		},
		ErrorLog: slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	return p
}

func (p *reverseProxy) serve(w http.ResponseWriter, r *http.Request, req request, token string) {
	ctx := context.WithValue(r.Context(), ctxRequest, req)
	ctx = context.WithValue(ctx, ctxToken, token)
	p.rp.ServeHTTP(w, r.WithContext(ctx))
}

func (p *reverseProxy) rewrite(pr *httputil.ProxyRequest) {
	req, _ := pr.In.Context().Value(ctxRequest).(request)
	token, _ := pr.In.Context().Value(ctxToken).(string)

	pr.Out.URL = p.s.upstreamURL(req, pr.In.URL.Query())
	pr.Out.Host = req.upstreamHost

	for _, h := range stripped {
		pr.Out.Header.Del(h)
	}

	switch req.kind {
	case kindGit:
		// The documented installation-token form for git over https, and the
		// one that needs no credential helper in the guest.
		pr.Out.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token)))
	default:
		pr.Out.Header.Set("Authorization", "Bearer "+token)
		if pr.Out.Header.Get("Accept") == "" {
			pr.Out.Header.Set("Accept", "application/vnd.github+json")
		}
		if pr.Out.Header.Get("X-GitHub-Api-Version") == "" {
			pr.Out.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		}
	}

	if pr.Out.Header.Get("User-Agent") == "" {
		pr.Out.Header.Set("User-Agent", "intproxy/"+p.s.version)
	}
}

var linkURLPattern = regexp.MustCompile(`<([^>]+)>`)

func (p *reverseProxy) modifyResponse(resp *http.Response) error {
	resp.Header.Del("Set-Cookie")

	// gh follows Link for pagination. Unrewritten, the guest chases
	// api.github.com with no credential and silently truncates at page one.
	if link := resp.Header.Get("Link"); link != "" {
		resp.Header.Set("Link", linkURLPattern.ReplaceAllStringFunc(link, func(m string) string {
			return "<" + p.rewriteURL(m[1:len(m)-1]) + ">"
		}))
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		resp.Header.Set("Location", p.rewriteURL(loc))
	}
	return nil
}

func (p *reverseProxy) rewriteURL(u string) string {
	self := p.s.selfURL()
	if rest, ok := strings.CutPrefix(u, "https://"+p.s.cfg.GitHub.apiHost()); ok {
		return self + "/api/v3" + rest
	}
	if rest, ok := strings.CutPrefix(u, "https://"+p.s.cfg.GitHub.gitHost()); ok {
		return self + rest
	}
	return u
}

func (p *reverseProxy) upstreamError(w http.ResponseWriter, r *http.Request, err error) {
	req, _ := r.Context().Value(ctxRequest).(request)
	p.s.log.Warn("upstream failed", "host", req.upstreamHost, "path", req.upstreamPath, "error", err)
	p.s.writeDenial(w, req.kind, http.StatusBadGateway, "intproxy: github could not be reached, so this request can be retried")
}
