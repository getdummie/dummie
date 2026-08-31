package dinit

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ImageConfigPath is what dclient writes into the rootfs from the os image row:
// the user, entrypoint, command and environment the container image declared.
// `docker export` keeps none of it, so without this file dinit has no way to
// know any of it -- and behaves as it did before it existed: root, no workload.
const ImageConfigPath = StateDir + "/image.json"

type imageConfig struct {
	User       string   `json:"user,omitempty"`
	Entrypoint []string `json:"entrypoint,omitempty"`
	Cmd        []string `json:"cmd,omitempty"`
	Env        []string `json:"env,omitempty"`
}

// image is read once at boot and then only read from: the console shell, every
// ssh session and the workload all take their environment and their user from
// it.
var image imageConfig

func readImageConfig(path string) imageConfig {
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("could not read %s: %v", path, err)
		}
		return imageConfig{}
	}
	var c imageConfig
	if err := json.Unmarshal(b, &c); err != nil {
		log.Printf("could not parse %s: %v", path, err)
		return imageConfig{}
	}
	return c
}

// argv is the workload: the entrypoint with the command as its arguments, which
// is what docker runs when both are set.
func (c imageConfig) argv() []string {
	return append(append([]string{}, c.Entrypoint...), c.Cmd...)
}

// defaultUser is who a console or an unqualified session belongs to. An image
// that declares no user, or one that does not resolve, stays on root as before.
func (c imageConfig) defaultUser() user {
	root := user{name: "root", home: "/root", shell: ""}
	if c.User == "" {
		return root
	}
	u, ok := resolveUser(c.User)
	if !ok {
		log.Printf("the image's user %q is not in /etc/passwd; falling back to root", c.User)
		return root
	}
	if u.home == "" {
		u.home = "/"
	}
	return u
}

// env builds the environment for a process running as u: the image's own
// variables, then the identity of the user, with a PATH always present. The
// image's PATH wins over the built-in one, since it is the one its binaries
// were installed against.
func (c imageConfig) env(u user, extra ...string) []string {
	shell := u.shell
	if shell == "" {
		shell = findShell()
	}
	env := append([]string{pathEnv}, c.Env...)
	env = append(env,
		"HOME="+u.home,
		"USER="+u.name,
		"LOGNAME="+u.name,
	)
	if shell != "" {
		env = append(env, "SHELL="+shell)
	}
	return dedupEnv(append(env, extra...))
}

// dedupEnv keeps the last assignment of each name, so a caller can append an
// override rather than having to filter what came before. Order is otherwise
// preserved: PATH stays where the image put it.
func dedupEnv(env []string) []string {
	last := make(map[string]int, len(env))
	for i, e := range env {
		if name, _, ok := strings.Cut(e, "="); ok {
			last[name] = i
		}
	}
	out := make([]string, 0, len(env))
	for i, e := range env {
		name, _, ok := strings.Cut(e, "=")
		if !ok || last[name] == i {
			out = append(out, e)
		}
	}
	return out
}

// writeEnvironment puts the image's variables where the things dinit does not
// spawn itself will still find them: /etc/environment for pam logins, and a
// profile snippet for login shells. Both are best-effort -- an image may have
// neither convention -- and only the image's own variables go in, not the
// per-user identity, which the login itself sets.
func writeEnvironment(c imageConfig) {
	if len(c.Env) == 0 {
		return
	}
	var etc, profile strings.Builder
	etc.WriteString("# Written by dinit from the container image's configuration.\n")
	profile.WriteString("# Written by dinit from the container image's configuration.\n")
	for _, e := range dedupEnv(c.Env) {
		name, value, ok := strings.Cut(e, "=")
		if !ok || name == "" {
			continue
		}
		etc.WriteString(name + "=" + value + "\n")
		profile.WriteString("export " + name + "=" + shellQuote(value) + "\n")
	}
	writeFile("/etc/environment", etc.String())
	if err := os.MkdirAll("/etc/profile.d", 0o755); err == nil {
		writeFile("/etc/profile.d/dinit-image.sh", profile.String())
	}
}

// shellQuote makes a value safe to export from a sourced sh snippet: single
// quotes stop every expansion, and an embedded quote is closed and re-opened.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// superviseWorkload runs the image's entrypoint and command, as the image's
// user, for as long as the vm is up. It is restarted when it exits -- a crashed
// app comes back on its own, and ssh and the console stay reachable either way
// since the workload is just another child of pid 1.
func superviseWorkload(r *reaper, c imageConfig) {
	argv := c.argv()
	if len(argv) == 0 {
		log.Print("the image declares no entrypoint or command; nothing to run")
		return
	}
	u := c.defaultUser()
	if c.User != "" && u.uid == 0 && c.User != "root" {
		log.Printf("WARNING: the image asks for user %q but the workload is running as root", c.User)
	}
	log.Printf("running the image's workload as %s (uid %d): %s", u.name, u.uid, strings.Join(argv, " "))

	const (
		minBackoff = time.Second
		maxBackoff = 30 * time.Second
		settled    = time.Minute
	)
	backoff := minBackoff
	for {
		started := time.Now()
		pid, err := startWorkload(r, c, u, argv)
		if err != nil {
			// A missing binary or an unwritable log will not fix itself on a
			// retry, so this is reported once and the workload is given up on.
			// The vm stays up and reachable, which is what makes it debuggable.
			log.Printf("could not start the image's workload, and will not retry: %v", err)
			return
		}
		st := <-r.wait(pid)

		// A workload that stayed up is treated as healthy: the next crash gets
		// a prompt restart rather than the backoff the last one had reached.
		if ran := time.Since(started); ran >= settled {
			backoff = minBackoff
			log.Printf("the image's workload exited (%s) after %s; restarting in %s",
				exitDescription(st), ran.Round(time.Second), backoff)
		} else {
			log.Printf("the image's workload exited (%s) after %s; restarting in %s",
				exitDescription(st), ran.Round(time.Millisecond), backoff)
		}
		time.Sleep(backoff)
		if backoff < maxBackoff {
			if backoff *= 2; backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func startWorkload(r *reaper, c imageConfig, u user, argv []string) (int, error) {
	env := c.env(u)

	// ForkExec executes a path: unlike a shell, or os/exec, it never searches.
	// An image's entrypoint is usually a bare name, so it is resolved here the
	// way docker resolves it -- against the image's own PATH.
	path, err := resolveArgv0(argv[0], env)
	if err != nil {
		return 0, err
	}

	out, err := workloadLog()
	if err != nil {
		return 0, err
	}
	defer out.Close()

	// The workload has no terminal: it is a service, not a session, and reading
	// from the console would fight the shell that is already on it.
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer null.Close()

	dir := u.home
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		dir = "/"
	}

	attr := &syscall.ProcAttr{
		Dir:   dir,
		Env:   env,
		Files: []uintptr{null.Fd(), out.Fd(), out.Fd()},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	}
	if u.uid != 0 {
		attr.Sys.Credential = &syscall.Credential{Uid: uint32(u.uid), Gid: uint32(u.gid)}
	}
	// argv[0] stays as the image wrote it, which is what a shell would pass and
	// what busybox dispatches on.
	return r.spawn(path, argv, attr)
}

// resolveArgv0 finds the workload's binary. A name with a slash in it is used as
// given; a bare name is searched for on the image's PATH, which is the image's
// own and not dinit's.
func resolveArgv0(name string, env []string) (string, error) {
	if name == "" {
		return "", errors.New("the image's entrypoint is empty")
	}
	if strings.Contains(name, "/") {
		if !executable(name) {
			return "", fmt.Errorf("%s is not an executable file in this image", name)
		}
		return name, nil
	}
	search := envValue(env, "PATH")
	for _, dir := range filepath.SplitList(search) {
		if dir == "" {
			dir = "."
		}
		if p := filepath.Join(dir, name); executable(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("%q is not on the image's PATH (%s)", name, search)
}

// envValue reads a variable out of an already-built environment, taking the last
// assignment as exec does.
func envValue(env []string, name string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], name+"="); ok {
			return v
		}
	}
	return ""
}

// WorkloadLogPath is where the workload's output goes. It is not the console:
// the console is a shell an operator is using, and interleaving an app's stdout
// into it makes both unusable.
const WorkloadLogPath = "/var/log/dinit-workload.log"

var (
	workloadLogOnce sync.Once
	workloadLogFile *os.File
	workloadLogErr  error
)

func workloadLog() (*os.File, error) {
	workloadLogOnce.Do(func() {
		if err := os.MkdirAll(filepath.Dir(WorkloadLogPath), 0o755); err != nil {
			workloadLogErr = err
			return
		}
		workloadLogFile, workloadLogErr = os.OpenFile(WorkloadLogPath,
			os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	})
	if workloadLogErr != nil {
		return nil, workloadLogErr
	}
	// Every restart writes to the same file, so the handle is shared and the
	// caller's Close is a no-op on a duplicate.
	return dupFile(workloadLogFile)
}

func dupFile(f *os.File) (*os.File, error) {
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(fd)
	return os.NewFile(uintptr(fd), f.Name()), nil
}

func exitDescription(st syscall.WaitStatus) string {
	switch {
	case st.Signaled():
		return "killed by " + st.Signal().String()
	case st.Exited():
		return "status " + strconv.Itoa(st.ExitStatus())
	default:
		return "unknown"
	}
}
