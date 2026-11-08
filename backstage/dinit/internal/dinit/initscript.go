package dinit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// dclient hands the creator's init script over on a small read-only virtio disk:
// a header line naming the length, then the script. It runs once, as root, in
// the background, on the first boot only.
const (
	initScriptMagic    = "dummie-init-script v1 "
	initScriptMax      = 64 << 10
	InitScriptPath     = StateDir + "/init-script"
	InitScriptDonePath = StateDir + "/init-script-ran"
	InitScriptLog      = "/var/log/dummie-init.log"
	initScriptUnit     = "/etc/systemd/system/dummie-init.service"
	initScriptWants    = "/etc/systemd/system/multi-user.target.wants/dummie-init.service"
)

// stageInitScript copies a not-yet-run script off its disk into the rootfs and
// reports whether there is one waiting to run.
func stageInitScript() bool {
	if _, err := os.Stat(InitScriptDonePath); err == nil {
		return false
	}
	script, err := findInitScript("/sys/block", "/dev")
	if err != nil {
		log.Printf("could not read the init script: %v", err)
		return false
	}
	if script == nil {
		return false
	}
	if err := os.MkdirAll(StateDir, 0o755); err != nil {
		log.Printf("could not stage the init script: %v", err)
		return false
	}
	if err := os.WriteFile(InitScriptPath, script, 0o700); err != nil {
		log.Printf("could not stage the init script: %v", err)
		return false
	}
	return true
}

func findInitScript(sysBlock, dev string) ([]byte, error) {
	names, err := os.ReadDir(sysBlock)
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		if !strings.HasPrefix(n.Name(), "vd") {
			continue
		}
		script, err := readInitScript(filepath.Join(dev, n.Name()))
		if err != nil {
			return nil, err
		}
		if script != nil {
			return script, nil
		}
	}
	return nil, nil
}

func readInitScript(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	head := make([]byte, 64)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if !bytes.HasPrefix(head, []byte(initScriptMagic)) {
		return nil, nil
	}
	nl := bytes.IndexByte(head, '\n')
	if nl < 0 {
		return nil, errors.New("the init script disk has a broken header")
	}
	size, err := strconv.Atoi(string(head[len(initScriptMagic):nl]))
	if err != nil || size < 0 || size > initScriptMax {
		return nil, errors.New("the init script disk names an impossible length")
	}
	script := make([]byte, size)
	if _, err := f.ReadAt(script, int64(nl+1)); err != nil {
		return nil, err
	}
	return script, nil
}

// installInitUnit leaves the script to systemd on an image dinit hands off to.
func installInitUnit() {
	unit := "[Unit]\nDescription=dummie init script\nWants=network-online.target\n" +
		"After=network-online.target\nConditionPathExists=!" + InitScriptDonePath + "\n\n" +
		"[Service]\nType=simple\nExecStart=" + InitPath + " init-script\n\n" +
		"[Install]\nWantedBy=multi-user.target\n"
	if err := os.MkdirAll(filepath.Dir(initScriptWants), 0o755); err != nil {
		log.Printf("could not install the init script unit: %v", err)
		return
	}
	writeFile(initScriptUnit, unit)
	_ = os.Remove(initScriptWants)
	if err := os.Symlink(initScriptUnit, initScriptWants); err != nil {
		log.Printf("could not enable the init script unit: %v", err)
	}
}

// startInitScript runs it from a child, since pid 1 reaps everything and would
// steal the exit status from an exec.Cmd of its own.
func startInitScript(r *reaper) {
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		log.Printf("could not start the init script: %v", err)
		return
	}
	defer null.Close()
	attr := &syscall.ProcAttr{
		Dir:   "/",
		Env:   []string{pathEnv},
		Files: []uintptr{null.Fd(), null.Fd(), null.Fd()},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	}
	pid, err := r.spawn(InitPath, []string{InitPath, "init-script"}, attr)
	if err != nil {
		log.Printf("could not start the init script: %v", err)
		return
	}
	log.Printf("running the init script; its output goes to %s", InitScriptLog)
	go func() { <-r.wait(pid) }()
}

// RunInitScript is `dinit init-script`. The marker goes down first, so a script
// that hangs or crashes the guest is not run again on the next boot.
func RunInitScript() int {
	if _, err := os.Stat(InitScriptDonePath); err == nil {
		return 0
	}
	if _, err := os.Stat(InitScriptPath); err != nil {
		return 0
	}
	if err := os.WriteFile(InitScriptDonePath, nil, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "init-script: %v\n", err)
		return 1
	}
	_ = os.MkdirAll(filepath.Dir(InitScriptLog), 0o755)
	out, err := os.OpenFile(InitScriptLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init-script: %v\n", err)
		return 1
	}
	defer out.Close()

	fmt.Fprintf(out, "== init script started %s\n", time.Now().UTC().Format(time.RFC3339))
	argv := initScriptArgv()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Env = []string{pathEnv, "HOME=/root", "USER=root", "LOGNAME=root"}
	cmd.Dir = "/"
	if fi, err := os.Stat("/root"); err == nil && fi.IsDir() {
		cmd.Dir = "/root"
	}
	err = cmd.Run()
	status := "exited 0"
	if err != nil {
		status = err.Error()
	}
	fmt.Fprintf(out, "== init script finished %s: %s\n", time.Now().UTC().Format(time.RFC3339), status)
	return 0
}

// initScriptArgv honours a shebang and otherwise hands the script to a shell.
func initScriptArgv() []string {
	if b, err := os.ReadFile(InitScriptPath); err == nil && bytes.HasPrefix(b, []byte("#!")) {
		return []string{InitScriptPath}
	}
	if sh := findShell(); sh != "" {
		return []string{sh, InitScriptPath}
	}
	return []string{"/bin/sh", InitScriptPath}
}
