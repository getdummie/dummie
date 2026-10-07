package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// harness is one coding agent cli the daemon can drive.
type harness interface {
	name() string
	binary() string
	// steers says a prompt sent mid-turn reaches the running turn; for the
	// rest it waits for the turn to end.
	steers() bool
	renames() bool
	// presetModels is what can be picked before a session is running to ask.
	presetModels() []listedModel
	list(d *daemon) []sessionSummary
	// start resumes the session id, or starts a new one in cwd if id is empty.
	start(d *daemon, id, cwd string, model *modelRef) (proc, error)
}

func (d *daemon) harnessFor(name string) (harness, error) {
	for _, h := range d.harnesses {
		if h.name() == name {
			return h, nil
		}
	}
	return nil, fmt.Errorf("harness %q is not supported", name)
}

// userBins are where installers put binaries; ssh runs attach without a
// login shell, so the image's PATH alone misses them.
var userBins = []string{".bun/bin", ".local/bin", ".opencode/bin", ".npm-global/bin"}

// withUserBins puts them ahead of path. Missing dirs are kept, so an install
// made while the daemon runs is found without a restart.
func withUserBins(home, path string) string {
	have := map[string]bool{}
	for _, d := range filepath.SplitList(path) {
		have[d] = true
	}
	var dirs []string
	for _, b := range userBins {
		if d := filepath.Join(home, b); !have[d] {
			dirs = append(dirs, d)
		}
	}
	if path != "" {
		dirs = append(dirs, path)
	}
	return strings.Join(dirs, string(filepath.ListSeparator))
}

func available(h harness) error {
	if _, err := exec.LookPath(h.binary()); err != nil {
		return fmt.Errorf("%s is not installed in this vm", h.binary())
	}
	return nil
}

// proxyInfo is the fleet's llm proxy as attach last saw it. With no file the
// harnesses use their own logins.
type proxyInfo struct {
	Base   string     `json:"base"`
	Models []llmModel `json:"models"`
}

func proxyFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dummie", "llm.json")
}

func saveProxy(base string, models []llmModel) error {
	b, err := json.Marshal(proxyInfo{Base: base, Models: models})
	if err != nil {
		return err
	}
	tmp := proxyFile() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, proxyFile())
}

func forgetProxy() {
	_ = os.Remove(proxyFile())
}

func loadProxy() (proxyInfo, bool) {
	var p proxyInfo
	b, err := os.ReadFile(proxyFile())
	if err != nil || json.Unmarshal(b, &p) != nil || p.Base == "" {
		return proxyInfo{}, false
	}
	return p, true
}

// A chatgpt subscription is only served as the responses api; everything else
// the proxy has speaks chat completions and anthropic messages.
func (p proxyInfo) responsesModels() []string {
	var out []string
	for _, m := range p.Models {
		if m.OwnedBy == "chatgpt" {
			out = append(out, m.ID)
		}
	}
	return out
}

func (p proxyInfo) chatModels() []string {
	var out []string
	for _, m := range p.Models {
		if m.OwnedBy != "chatgpt" {
			out = append(out, m.ID)
		}
	}
	return out
}

func listed(provider string, ids []string) []listedModel {
	out := make([]listedModel, 0, len(ids))
	for _, id := range ids {
		out = append(out, listedModel{Provider: provider, ID: id})
	}
	return out
}

// pickModel keeps a requested model if the list has it, else the first.
func pickModel(want *modelRef, ids []string) string {
	if want != nil {
		for _, id := range ids {
			if id == want.ID {
				return id
			}
		}
	}
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// withoutEnv drops the named variables from the daemon's environment.
func withoutEnv(names ...string) []string {
	var out []string
outer:
	for _, kv := range os.Environ() {
		for _, n := range names {
			if strings.HasPrefix(kv, n+"=") {
				continue outer
			}
		}
		out = append(out, kv)
	}
	return out
}

func checkDir(cwd string) error {
	if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
		return errors.New(cwd + " is not a directory")
	}
	return nil
}

// withFiles adds non-image uploads to the text as paths the agent can read.
func withFiles(text string, files []string) string {
	if len(files) == 0 {
		return text
	}
	return text + "\n\nAttached files:\n- " + strings.Join(files, "\n- ")
}
