package agent

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Uploads live outside the session's directory so they never show up in its
// diff, and arrive in base64 chunks small enough for dpipe's websocket.
const maxUpload = 25 << 20

type upload struct {
	f    *os.File
	path string
	size int
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (d *daemon) uploadDir() string {
	return filepath.Join(d.home, ".dummie", "uploads", time.Now().Format("2006-01-02"))
}

func (d *daemon) ownsUpload(path string) bool {
	root := filepath.Join(d.home, ".dummie", "uploads") + string(filepath.Separator)
	clean := filepath.Clean(path)
	return strings.HasPrefix(clean, root)
}

func safeName(name string) string {
	name = unsafeName.ReplaceAllString(filepath.Base(name), "_")
	name = strings.TrimLeft(name, ".")
	if name == "" {
		name = "file"
	}
	return name
}

func (c *client) upload(r request) {
	if r.Req == "" {
		c.fail(r, errors.New("an upload needs a req id"))
		return
	}
	chunk, err := base64.StdEncoding.DecodeString(r.Data)
	if err != nil {
		c.fail(r, fmt.Errorf("upload chunk is not base64: %w", err))
		return
	}

	c.mu.Lock()
	u, ok := c.uploads[r.Req]
	c.mu.Unlock()
	if !ok {
		dir := c.d.uploadDir()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			c.fail(r, err)
			return
		}
		path := filepath.Join(dir, fmt.Sprintf("%d-%s", time.Now().UnixNano(), safeName(r.Name)))
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			c.fail(r, err)
			return
		}
		u = &upload{f: f, path: path}
		c.mu.Lock()
		c.uploads[r.Req] = u
		c.mu.Unlock()
	}

	u.size += len(chunk)
	if u.size > maxUpload {
		c.dropUpload(r.Req, u)
		c.fail(r, fmt.Errorf("uploads are limited to %d MiB", maxUpload>>20))
		return
	}
	if _, err := u.f.Write(chunk); err != nil {
		c.dropUpload(r.Req, u)
		c.fail(r, err)
		return
	}
	if !r.Last {
		c.send(map[string]any{"t": "upload_ack", "req": r.Req})
		return
	}
	c.mu.Lock()
	delete(c.uploads, r.Req)
	c.mu.Unlock()
	if err := u.f.Close(); err != nil {
		_ = os.Remove(u.path)
		c.fail(r, err)
		return
	}
	c.send(map[string]any{"t": "uploaded", "req": r.Req, "path": u.path})
}

func (c *client) dropUpload(req string, u *upload) {
	c.mu.Lock()
	delete(c.uploads, req)
	c.mu.Unlock()
	_ = u.f.Close()
	_ = os.Remove(u.path)
}

func (c *client) abortUploads() {
	c.mu.Lock()
	ups := c.uploads
	c.uploads = map[string]*upload{}
	c.mu.Unlock()
	for _, u := range ups {
		_ = u.f.Close()
		_ = os.Remove(u.path)
	}
}
