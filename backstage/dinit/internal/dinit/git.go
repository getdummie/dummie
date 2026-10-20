package dinit

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// GitConfiguredPath marks the creator's git identity as applied, so a reboot
// does not undo whatever was changed inside the guest since.
const GitConfiguredPath = StateDir + "/git-configured"

func configureGit(p params, c imageConfig) {
	if p.gitName == "" && p.gitEmail == "" {
		return
	}
	if _, err := os.Stat(GitConfiguredPath); err == nil {
		return
	}
	u := c.defaultUser()
	if err := applyGitIdentity(p, c, u); err != nil {
		log.Printf("could not set the git identity for %s: %v", u.name, err)
	} else {
		log.Printf("set the git identity for %s", u.name)
	}
	if err := os.MkdirAll(StateDir, 0o755); err == nil {
		writeFile(GitConfiguredPath, "")
	}
}

// applyGitIdentity uses the image's git when it has one. Without it a fresh
// ~/.gitconfig is written instead, so a git installed later still picks it up.
func applyGitIdentity(p params, c imageConfig, u user) error {
	env := c.env(u)
	git, err := resolveArgv0("git", env)
	if err != nil {
		return writeGitConfig(p, u)
	}
	dir := u.home
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		dir = "/"
	}
	for _, kv := range [][2]string{{"user.name", p.gitName}, {"user.email", p.gitEmail}} {
		if kv[1] == "" {
			continue
		}
		cmd := exec.Command(git, "config", "--global", kv[0], kv[1])
		cmd.Env = env
		cmd.Dir = dir
		if u.uid != 0 {
			cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(u.uid), Gid: uint32(u.gid)}}
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git config %s: %v: %s", kv[0], err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func writeGitConfig(p params, u user) error {
	if u.home == "" || u.home == "/" {
		return errors.New("git is not installed and the user has no home directory")
	}
	path := filepath.Join(u.home, ".gitconfig")
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("git is not installed and %s already exists", path)
	}
	var b strings.Builder
	b.WriteString("[user]\n")
	if p.gitName != "" {
		b.WriteString("\tname = " + gitQuote(p.gitName) + "\n")
	}
	if p.gitEmail != "" {
		b.WriteString("\temail = " + gitQuote(p.gitEmail) + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Chown(path, u.uid, u.gid)
}

func gitQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
