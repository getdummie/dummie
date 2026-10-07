package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const startWait = 5 * time.Second

func stateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".dummie")
	return dir, os.MkdirAll(dir, 0o700)
}

// binaryID names this build by version and by the binary's inode: a local
// build always says "dev", and an upgrade renames a new file into place.
func binaryID(version string) string {
	self, err := os.Executable()
	if err != nil {
		return version
	}
	var st syscall.Stat_t
	if syscall.Stat(self, &st) != nil {
		return version
	}
	return fmt.Sprintf("%s-%x", version, st.Ino)
}

// The socket carries the binary's id so an upgraded dinit starts its own
// daemon instead of talking to one built from the binary it replaced.
func socketPath(dir, id string) string {
	return filepath.Join(dir, "agent-"+id+".sock")
}

// retireOthers asks every daemon from another binary to exit once its agents
// are done, so two daemons never keep driving the same sessions.
func retireOthers(dir, id string) {
	own := socketPath(dir, id)
	socks, _ := filepath.Glob(filepath.Join(dir, "agent-*.sock"))
	for _, s := range socks {
		if s == own {
			continue
		}
		c, err := net.DialTimeout("unix", s, time.Second)
		if errors.Is(err, syscall.ECONNREFUSED) {
			_ = os.Remove(s)
			continue
		}
		if err != nil {
			continue
		}
		_ = c.SetDeadline(time.Now().Add(2 * time.Second))
		_ = WriteFrame(c, OpText, []byte(`{"t":"retire"}`))
		_ = c.Close()
		log.Printf("agent: asked the daemon on %s to retire", s)
	}
}

// Attach is what dpipe runs over ssh: it makes sure the daemon is up, then
// relays frames between its stdio and the daemon. Stdout and stderr share one
// pipe under dinit's ssh server, so nothing but frames may be written to either.
func Attach(version string, args []string) int {
	tld := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--tld" && i+1 < len(args) {
			tld = args[i+1]
			i++
		}
	}
	dir, err := stateDir()
	if err != nil {
		return attachFail("agent_start", err)
	}
	logf, err := os.OpenFile(filepath.Join(dir, "agent.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return attachFail("agent_start", err)
	}
	defer logf.Close()
	log.SetOutput(logf)

	if tld != "" {
		if err := refreshModels(tld); err != nil {
			log.Printf("agent: models.json not refreshed: %v", err)
		}
	}

	id := binaryID(version)
	conn, err := dialOrStart(dir, id, logf)
	if err != nil {
		return attachFail("agent_start", err)
	}
	defer conn.Close()
	retireOthers(dir, id)

	go func() {
		_, _ = io.Copy(conn, os.Stdin)
		_ = conn.(*net.UnixConn).CloseWrite()
	}()
	_, _ = io.Copy(os.Stdout, conn)
	return 0
}

func attachFail(code string, err error) int {
	b, _ := json.Marshal(map[string]string{"t": "error", "code": code, "message": err.Error()})
	_ = WriteFrame(os.Stdout, OpText, b)
	return 1
}

func dialOrStart(dir, id string, logf *os.File) (net.Conn, error) {
	sock := socketPath(dir, id)
	if c, err := net.Dial("unix", sock); err == nil {
		return c, nil
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return nil, err
	}
	defer devnull.Close()
	cmd := exec.Command(self, "agent", "serve")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not start the agent daemon: %w", err)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(startWait)
	for time.Now().Before(deadline) {
		if c, err := net.Dial("unix", sock); err == nil {
			return c, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, errors.New("the agent daemon did not come up; see ~/.dummie/agent.log in the vm")
}
