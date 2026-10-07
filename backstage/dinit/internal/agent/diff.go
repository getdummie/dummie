package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	diffEvery      = 2 * time.Second
	gitTimeout     = 10 * time.Second
	maxPatch       = 2 << 20
	maxTreePaths   = 5000
	maxUntracked   = 50
	maxUntrackedSz = 256 << 10
)

// watch polls one directory's git state for the tabs looking at it, and pushes
// a snapshot only when it changed. pi's tool events poke it between polls.
type watch struct {
	cwd     string
	clients map[*client]struct{}
	poke    chan struct{}
	stop    chan struct{}
	last    [32]byte
	lastMsg []byte
}

type diffSnapshot struct {
	T         string   `json:"t"`
	Cwd       string   `json:"cwd"`
	Repo      bool     `json:"repo"`
	Branch    string   `json:"branch,omitempty"`
	Patch     string   `json:"patch"`
	Truncated bool     `json:"truncated,omitempty"`
	Tree      []string `json:"tree"`
	Error     string   `json:"error,omitempty"`
}

func (d *daemon) watchFor(cwd string, c *client) {
	d.mu.Lock()
	w, ok := d.watches[cwd]
	if !ok {
		w = &watch{cwd: cwd, clients: map[*client]struct{}{}, poke: make(chan struct{}, 1), stop: make(chan struct{})}
		d.watches[cwd] = w
		go d.runWatch(w)
	}
	w.clients[c] = struct{}{}
	last := w.lastMsg
	d.mu.Unlock()
	if last != nil {
		c.sendRaw(last)
	}
	d.poke(cwd)
}

func (d *daemon) unwatch(cwd string, c *client) {
	d.mu.Lock()
	defer d.mu.Unlock()
	w, ok := d.watches[cwd]
	if !ok {
		return
	}
	delete(w.clients, c)
	if len(w.clients) == 0 {
		delete(d.watches, cwd)
		close(w.stop)
	}
}

func (d *daemon) poke(cwd string) {
	d.mu.Lock()
	w, ok := d.watches[cwd]
	d.mu.Unlock()
	if !ok {
		return
	}
	select {
	case w.poke <- struct{}{}:
	default:
	}
}

func (d *daemon) runWatch(w *watch) {
	t := time.NewTicker(diffEvery)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
		case <-w.poke:
		}
		snap := snapshot(w.cwd)
		b, err := json.Marshal(snap)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(b)

		d.mu.Lock()
		if sum == w.last {
			d.mu.Unlock()
			continue
		}
		w.last, w.lastMsg = sum, b
		for c := range w.clients {
			c.sendRaw(b)
		}
		d.mu.Unlock()
	}
}

func git(cwd string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", cwd, "--no-pager"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	return cmd.Output()
}

func snapshot(cwd string) diffSnapshot {
	s := diffSnapshot{T: "diff", Cwd: cwd, Tree: []string{}}
	if _, err := exec.LookPath("git"); err != nil {
		s.Error = "git is not installed in this vm"
		return s
	}
	if out, err := git(cwd, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(string(out)) != "true" {
		return s
	}
	s.Repo = true
	if out, err := git(cwd, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		s.Branch = strings.TrimSpace(string(out))
	}

	var patch bytes.Buffer
	base := "HEAD"
	if _, err := git(cwd, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		// No commit yet: everything staged is new, so diff the index against
		// the empty tree.
		base = emptyTree
	}
	if out, err := git(cwd, "diff", base, "--no-color", "--no-ext-diff", "--relative"); err == nil {
		patch.Write(out)
	} else {
		s.Error = gitError(err)
	}

	untracked, _ := git(cwd, "ls-files", "--others", "--exclude-standard", "-z")
	for i, rel := range splitZ(untracked) {
		if i >= maxUntracked || patch.Len() > maxPatch {
			break
		}
		if fi, err := os.Stat(filepath.Join(cwd, rel)); err != nil || !fi.Mode().IsRegular() || fi.Size() > maxUntrackedSz {
			continue
		}
		// --no-index exits 1 when the files differ, which they always do here.
		out, _ := git(cwd, "diff", "--no-index", "--no-color", "--no-ext-diff", "--", "/dev/null", rel)
		patch.Write(out)
	}
	if patch.Len() > maxPatch {
		s.Truncated = true
		s.Patch = patch.String()[:maxPatch]
	} else {
		s.Patch = patch.String()
	}

	if out, err := git(cwd, "ls-files", "--cached", "--others", "--exclude-standard", "-z"); err == nil {
		tree := splitZ(out)
		if len(tree) > maxTreePaths {
			tree, s.Truncated = tree[:maxTreePaths], true
		}
		s.Tree = tree
	}
	return s
}

const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

func splitZ(b []byte) []string {
	var out []string
	for _, p := range bytes.Split(b, []byte{0}) {
		if len(p) > 0 {
			out = append(out, string(p))
		}
	}
	return out
}

func gitError(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return strings.TrimSpace(string(ee.Stderr))
	}
	return err.Error()
}
