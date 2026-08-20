package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
)

// webFS carries the generated SPA. `all:` because Nuxt writes its assets into a
// directory whose name starts with an underscore, which a bare embed skips.
//
//go:embed all:web
var webFS embed.FS

// webRoot returns the embedded SPA, or false when this binary was built without
// one -- a dev build, where placeholder.txt is the only thing in web/. Which of
// the two it is decides whether serve runs the Nuxt dev server or serves itself.
func webRoot() (fs.FS, bool) {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}

// serveSPA answers every request the API does not own out of the embedded
// build. The bypass list is the same one proxyToNuxt uses, for the same reasons.
func serveSPA(root fs.FS) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			if path := req.URL.Path; strings.HasPrefix(path, "/api/v1") || path == proxyLoginPath {
				return next(c)
			}
			http.ServeFileFS(c.Response(), req, root, spaFile(root, req.URL.Path))
			return nil
		}
	}
}

// spaFile maps a request path to a file in the build. Anything with nothing
// behind it resolves to index.html -- and so answers 200, not 404 -- because the
// SPA owns its routes: /vms/123 is a page vue-router draws after the shell has
// loaded, not a document this server has.
func spaFile(root fs.FS, urlPath string) string {
	name := strings.Trim(urlPath, "/")
	if name == "" {
		return "index.html"
	}
	st, err := fs.Stat(root, name)
	if err != nil {
		return "index.html"
	}
	if !st.IsDir() {
		return name
	}
	// `nuxt generate` emits one directory per route holding its own index.html.
	if idx := name + "/index.html"; fileExists(root, idx) {
		return idx
	}
	return "index.html"
}

func fileExists(root fs.FS, name string) bool {
	st, err := fs.Stat(root, name)
	return err == nil && !st.IsDir()
}
