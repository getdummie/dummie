package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
)

//go:embed all:web
var webFS embed.FS

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
	if idx := name + "/index.html"; fileExists(root, idx) {
		return idx
	}
	return "index.html"
}

func fileExists(root fs.FS, name string) bool {
	st, err := fs.Stat(root, name)
	return err == nil && !st.IsDir()
}
