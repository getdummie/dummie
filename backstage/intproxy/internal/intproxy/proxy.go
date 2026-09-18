package intproxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"intproxy/internal/credential"
	"intproxy/internal/integration"
)

type ctxKey int

const (
	ctxIntegration ctxKey = iota
	ctxRoute
	ctxToken
)

// stripped headers carry identity a client must not be able to smuggle
// upstream, and that we replace with our own credential. Nothing
// forwarded-ish goes out either: the upstream has no business learning a
// client's internal address.
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

func (s *Server) newReverseProxy() *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite:        s.rewrite,
		ModifyResponse: s.modifyResponse,
		ErrorHandler:   s.upstreamError,
		// Flush every write: git's sideband progress and any streaming api
		// response both stall behind a buffer.
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
}

func (s *Server) proxy(w http.ResponseWriter, r *http.Request, ig integration.Integration, route integration.Route, tok credential.Token) {
	ctx := context.WithValue(r.Context(), ctxIntegration, ig)
	ctx = context.WithValue(ctx, ctxRoute, route)
	ctx = context.WithValue(ctx, ctxToken, tok)
	s.rp.ServeHTTP(w, r.WithContext(ctx))
}

func (s *Server) rewrite(pr *httputil.ProxyRequest) {
	ig, _ := pr.In.Context().Value(ctxIntegration).(integration.Integration)
	route, _ := pr.In.Context().Value(ctxRoute).(integration.Route)
	tok, _ := pr.In.Context().Value(ctxToken).(credential.Token)

	out := &url.URL{Scheme: "https", Host: route.UpstreamHost, Path: route.UpstreamPath}
	if q := pr.In.URL.Query(); len(q) > 0 {
		out.RawQuery = q.Encode()
	}
	pr.Out.URL = out
	pr.Out.Host = route.UpstreamHost

	for _, h := range stripped {
		pr.Out.Header.Del(h)
	}
	if ig != nil {
		ig.Apply(pr, route, tok)
	}
}

func (s *Server) modifyResponse(resp *http.Response) error {
	ctx := resp.Request.Context()
	ig, _ := ctx.Value(ctxIntegration).(integration.Integration)
	route, _ := ctx.Value(ctxRoute).(integration.Route)
	if ig == nil {
		return nil
	}

	// A 401 from upstream is about our credential, not the caller's, and git
	// answers a 401 by prompting for a username and password. Turn it into our
	// own refusal so the person reading it learns what is actually wrong.
	if resp.StatusCode == http.StatusUnauthorized {
		s.log.Warn("upstream rejected this proxy's credential; it is expired or revoked",
			"integration", ig.Name(), "resource", route.Resource)
		return replaceWithError(resp, ig, route, http.StatusForbidden,
			"intproxy: this proxy's own credential was rejected upstream, so it has expired or been revoked; nothing is wrong with your request")
	}
	return ig.ModifyResponse(resp)
}

// replaceWithError swaps the whole response, headers included, so nothing from
// upstream survives -- notably WWW-Authenticate, which would make a client
// prompt just as a 401 does.
func replaceWithError(resp *http.Response, ig integration.Integration, route integration.Route, status int, msg string) error {
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	ct, body := ig.RenderError(route, status, msg)

	resp.StatusCode = status
	resp.Status = fmt.Sprintf("%d %s", status, http.StatusText(status))
	resp.Header = http.Header{}
	resp.Header.Set("Content-Type", ct)
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.TransferEncoding = nil
	resp.Uncompressed = false
	return nil
}

func (s *Server) upstreamError(w http.ResponseWriter, r *http.Request, err error) {
	ig, _ := r.Context().Value(ctxIntegration).(integration.Integration)
	route, _ := r.Context().Value(ctxRoute).(integration.Route)
	s.log.Warn("upstream failed", "host", route.UpstreamHost, "path", route.UpstreamPath, "error", err)
	if ig == nil {
		http.Error(w, "intproxy: the upstream could not be reached", http.StatusBadGateway)
		return
	}
	s.writeError(w, ig, route, http.StatusBadGateway,
		"intproxy: the upstream could not be reached, so this request can be retried")
}
