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

// Chat attachments live outside the session's directory so they never show up
// in its diff; uploads with a dest land there instead. Both arrive in base64
// chunks small enough for dpipe's websocket.
const maxUpload = 1 << 30

type upload struct {
	f     *os.File
	path  string
	final string
	size  int
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

var errExists = errors.New("a file with that name already exists there")

func (c *client) startUpload(r request) (*upload, error) {
	if r.Dest == "" {
		dir := c.d.uploadDir()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		path := filepath.Join(dir, fmt.Sprintf("%d-%s", time.Now().UnixNano(), safeName(r.Name)))
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		return &upload{f: f, path: path, final: path}, nil
	}

	dest := c.d.expandHome(r.Dest)
	if !filepath.IsAbs(dest) {
		return nil, fmt.Errorf("%s is not an absolute path", r.Dest)
	}
	if fi, err := os.Stat(dest); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", r.Dest)
	}
	name := safeName(r.Name)
	final := filepath.Join(dest, name)
	if _, err := os.Lstat(final); err == nil && !r.Overwrite {
		return nil, errExists
	}
	tmp := filepath.Join(dest, fmt.Sprintf(".%s.%d.part", name, time.Now().UnixNano()))
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &upload{f: f, path: tmp, final: final}, nil
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
		u, err = c.startUpload(r)
		if errors.Is(err, errExists) {
			c.send(map[string]any{"t": "error", "req": r.Req, "code": "exists", "message": err.Error()})
			return
		}
		if err != nil {
			c.fail(r, err)
			return
		}
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
	if u.final != u.path {
		if err := os.Rename(u.path, u.final); err != nil {
			_ = os.Remove(u.path)
			c.fail(r, err)
			return
		}
	}
	c.send(map[string]any{"t": "uploaded", "req": r.Req, "path": u.final})
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
