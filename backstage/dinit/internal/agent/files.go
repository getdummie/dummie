package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The editor reads and writes text files inside a session's directory only.
// Writes arrive in chunks, since dpipe takes at most 1 MiB per message.
const (
	maxEditable = 1 << 20
	sniffBytes  = 8 << 10
)

// Media for the viewer is pulled in raw chunks, one request per chunk.
const (
	maxRaw   = 64 << 20
	rawChunk = 512 << 10
)

var errConflict = errors.New("the file changed on disk since it was opened")

// expandHome lets the working directory field take ~/... like a shell.
func (d *daemon) expandHome(p string) string {
	if p == "~" {
		return d.home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(d.home, p[2:])
	}
	return p
}

// resolveIn maps a path relative to root onto disk, refusing anything that
// leaves root, symlinks included.
func resolveIn(root, rel string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(realRoot, filepath.Clean("/"+rel))
	if abs == realRoot {
		return "", errors.New("that is the directory itself, not a file in it")
	}
	realDir, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	if realDir != realRoot && !strings.HasPrefix(realDir, realRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside %s", rel, root)
	}
	abs = filepath.Join(realDir, filepath.Base(abs))
	if fi, err := os.Lstat(abs); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is a symlink; edit its target instead", rel)
	}
	return abs, nil
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type fileContent struct {
	Content string `json:"content"`
	Hash    string `json:"hash"`
	Exists  bool   `json:"exists"`
	// Base is the file at HEAD, for editing inside a diff.
	Base       string `json:"base"`
	BaseExists bool   `json:"baseExists"`
}

func readEditable(root, rel string) (fileContent, error) {
	abs, err := resolveIn(root, rel)
	if err != nil {
		return fileContent{}, err
	}
	var out fileContent
	b, err := os.ReadFile(abs)
	switch {
	case err == nil:
		if err := checkText(b); err != nil {
			return fileContent{}, err
		}
		out.Content, out.Hash, out.Exists = string(b), hashOf(b), true
	case !errors.Is(err, fs.ErrNotExist):
		return fileContent{}, err
	}
	if base, err := git(root, "show", "HEAD:./"+filepath.ToSlash(filepath.Clean(rel))); err == nil && checkText(base) == nil {
		out.Base, out.BaseExists = string(base), true
	}
	return out, nil
}

func readRawChunk(root, rel string, off int64) ([]byte, int64, error) {
	abs, err := resolveIn(root, rel)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	size := fi.Size()
	switch {
	case !fi.Mode().IsRegular():
		return nil, 0, fmt.Errorf("%s is not a regular file", rel)
	case size > maxRaw:
		return nil, 0, fmt.Errorf("files over %d MiB cannot be previewed here", maxRaw>>20)
	case off < 0 || off > size:
		return nil, 0, fmt.Errorf("offset %d is outside the file", off)
	}
	buf := make([]byte, min(rawChunk, size-off))
	n, err := f.ReadAt(buf, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, err
	}
	return buf[:n], size, nil
}

func checkText(b []byte) error {
	if len(b) > maxEditable {
		return fmt.Errorf("files over %d KiB cannot be edited here", maxEditable>>10)
	}
	if bytes.IndexByte(b[:min(len(b), sniffBytes)], 0) >= 0 {
		return errors.New("this looks like a binary file")
	}
	return nil
}

// writeEditable replaces the file only if it still has the hash the editor
// started from; an empty hash means the file must not exist yet.
func writeEditable(root, rel, wantHash string, force bool, content []byte) (string, error) {
	abs, err := resolveIn(root, rel)
	if err != nil {
		return "", err
	}
	mode := fs.FileMode(0o644)
	cur, err := os.ReadFile(abs)
	switch {
	case err == nil:
		if !force && hashOf(cur) != wantHash {
			return "", errConflict
		}
		if fi, err := os.Stat(abs); err == nil {
			mode = fi.Mode().Perm()
		}
	case errors.Is(err, fs.ErrNotExist):
		if !force && wantHash != "" {
			return "", errConflict
		}
	default:
		return "", err
	}

	tmp, err := os.CreateTemp(filepath.Dir(abs), ".dummie-edit-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), abs); err != nil {
		return "", err
	}
	return hashOf(content), nil
}
