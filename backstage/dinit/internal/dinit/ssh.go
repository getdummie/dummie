package dinit

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

// The agent carries its own ssh server so that an image with no openssh-server
// still answers dproxy and dpipe. Shells, exec and tcp forwarding are supported;
// sftp is not, so scp against a bare image will not work.
const (
	sshPort       = 22
	hostKeyFile   = "ssh_host_ed25519_key"
	authKeysFile  = "authorized_keys"
	sshdConnLimit = 32
)

func serveSSH(r *reaper) {
	keys, err := authorizedKeys()
	if err != nil {
		log.Printf("ssh: %v", err)
		return
	}
	signer, err := hostKey()
	if err != nil {
		log.Printf("ssh: %v", err)
		return
	}

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(cm ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if _, ok := lookupUser(cm.User()); !ok {
				return nil, fmt.Errorf("no such user %q", cm.User())
			}
			marshalled := key.Marshal()
			for _, k := range keys {
				if bytes.Equal(k.Marshal(), marshalled) {
					return nil, nil
				}
			}
			return nil, fmt.Errorf("key not authorized")
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", sshPort))
	if err != nil {
		log.Printf("ssh: could not listen: %v", err)
		return
	}
	log.Printf("ssh on :%d (%d authorized key(s))", sshPort, len(keys))

	sem := make(chan struct{}, sshdConnLimit)
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Printf("ssh: accept: %v", err)
			return
		}
		select {
		case sem <- struct{}{}:
		default:
			log.Printf("ssh: too many connections; dropping %s", c.RemoteAddr())
			_ = c.Close()
			continue
		}
		go func() {
			defer func() { <-sem }()
			handleConn(r, cfg, c)
		}()
	}
}

func handleConn(r *reaper, cfg *ssh.ServerConfig, c net.Conn) {
	sconn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		log.Printf("ssh: handshake from %s failed: %v", c.RemoteAddr(), err)
		_ = c.Close()
		return
	}
	defer sconn.Close()
	go ssh.DiscardRequests(reqs)

	for nch := range chans {
		switch nch.ChannelType() {
		case "session":
			ch, chReqs, err := nch.Accept()
			if err != nil {
				continue
			}
			go (&session{r: r, ch: ch, user: sconn.User()}).serve(chReqs)
		case "direct-tcpip":
			go forwardTCP(nch)
		default:
			_ = nch.Reject(ssh.UnknownChannelType, nch.ChannelType())
		}
	}
}

type session struct {
	r    *reaper
	ch   ssh.Channel
	user string

	mu      sync.Mutex
	ptmx    *os.File
	pts     string
	term    string
	env     []string
	started bool
	pid     int
}

func (s *session) serve(reqs <-chan *ssh.Request) {
	defer s.ch.Close()
	for req := range reqs {
		var err error
		switch req.Type {
		case "pty-req":
			err = s.allocatePTY(req.Payload)
		case "env":
			var e struct{ Name, Value string }
			if err = ssh.Unmarshal(req.Payload, &e); err == nil {
				s.env = append(s.env, e.Name+"="+e.Value)
			}
		case "window-change":
			s.resize(req.Payload)
		case "shell":
			err = s.start(nil)
		case "exec":
			var e struct{ Command string }
			if err = ssh.Unmarshal(req.Payload, &e); err == nil {
				err = s.start([]string{"-c", e.Command})
			}
		case "signal":
			var sig struct{ Name string }
			if ssh.Unmarshal(req.Payload, &sig) == nil {
				s.signal(sig.Name)
			}
		default:
			err = fmt.Errorf("unsupported request %q", req.Type)
		}
		if req.WantReply {
			_ = req.Reply(err == nil, nil)
		}
		if err != nil && req.Type != "env" {
			log.Printf("ssh: %s: %v", req.Type, err)
		}
	}
}

func (s *session) allocatePTY(payload []byte) error {
	var p struct {
		Term                      string
		Cols, Rows, Width, Height uint32
		Modes                     string
	}
	if err := ssh.Unmarshal(payload, &p); err != nil {
		return err
	}
	ptmx, pts, err := openPTY()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ptmx, s.pts, s.term = ptmx, pts, p.Term
	s.mu.Unlock()
	setWinsize(ptmx, p.Cols, p.Rows, p.Width, p.Height)
	return nil
}

func (s *session) resize(payload []byte) {
	var w struct{ Cols, Rows, Width, Height uint32 }
	if ssh.Unmarshal(payload, &w) != nil {
		return
	}
	s.mu.Lock()
	ptmx := s.ptmx
	s.mu.Unlock()
	if ptmx != nil {
		setWinsize(ptmx, w.Cols, w.Rows, w.Width, w.Height)
	}
}

func (s *session) signal(name string) {
	sigs := map[string]syscall.Signal{
		"INT": unix.SIGINT, "TERM": unix.SIGTERM, "KILL": unix.SIGKILL,
		"HUP": unix.SIGHUP, "QUIT": unix.SIGQUIT,
	}
	sig, ok := sigs[name]
	s.mu.Lock()
	pid := s.pid
	s.mu.Unlock()
	if ok && pid > 0 {
		s.r.signal(pid, sig)
	}
}

func (s *session) start(args []string) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("the session already has a command")
	}
	s.started = true
	ptmx, pts, term := s.ptmx, s.pts, s.term
	s.mu.Unlock()

	u, ok := lookupUser(s.user)
	if !ok {
		return fmt.Errorf("no such user %q", s.user)
	}
	shell := u.shell
	if !executable(shell) {
		if shell = findShell(); shell == "" {
			return fmt.Errorf("the image has no usable shell")
		}
	}

	// The image's environment comes first so a session sees what the workload
	// sees; what the client pushed still wins over it, as does the pty's TERM.
	u.shell = shell
	extra := s.env
	if term != "" {
		extra = append(append([]string{}, s.env...), "TERM="+term)
	}
	env := image.env(u, extra...)

	attr := &syscall.ProcAttr{Dir: u.home, Env: env, Sys: &syscall.SysProcAttr{Setsid: true}}
	if u.uid != 0 {
		attr.Sys.Credential = &syscall.Credential{Uid: uint32(u.uid), Gid: uint32(u.gid)}
	}

	var closeAfterStart []*os.File
	var pipe func()

	if ptmx != nil {
		slave, err := os.OpenFile(pts, os.O_RDWR|unix.O_NOCTTY, 0)
		if err != nil {
			return err
		}
		if u.uid != 0 {
			_ = slave.Chown(u.uid, u.gid)
		}
		attr.Files = []uintptr{slave.Fd(), slave.Fd(), slave.Fd()}
		attr.Sys.Setctty = true
		attr.Sys.Ctty = 0
		closeAfterStart = []*os.File{slave}
		pipe = func() {
			go func() { _, _ = io.Copy(ptmx, s.ch) }()
			_, _ = io.Copy(s.ch, ptmx)
		}
	} else {
		stdinR, stdinW, err := os.Pipe()
		if err != nil {
			return err
		}
		outR, outW, err := os.Pipe()
		if err != nil {
			_ = stdinR.Close()
			_ = stdinW.Close()
			return err
		}
		attr.Files = []uintptr{stdinR.Fd(), outW.Fd(), outW.Fd()}
		closeAfterStart = []*os.File{stdinR, outW}
		pipe = func() {
			defer outR.Close()
			go func() {
				_, _ = io.Copy(stdinW, s.ch)
				_ = stdinW.Close()
			}()
			_, _ = io.Copy(s.ch, outR)
		}
	}

	pid, err := s.r.spawn(shell, shellArgv(shell, args), attr)
	for _, f := range closeAfterStart {
		_ = f.Close()
	}
	if err != nil {
		if ptmx != nil {
			_ = ptmx.Close()
		}
		return err
	}
	s.mu.Lock()
	s.pid = pid
	s.mu.Unlock()

	exit := s.r.wait(pid)
	go func() {
		defer s.ch.Close()
		pipe()
		ws := <-exit
		status := uint32(0)
		switch {
		case ws.Signaled():
			status = 128 + uint32(ws.Signal())
		case ws.ExitStatus() > 0:
			status = uint32(ws.ExitStatus())
		}
		_, _ = s.ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
		if ptmx != nil {
			_ = ptmx.Close()
		}
	}()
	return nil
}

func forwardTCP(nch ssh.NewChannel) {
	var req struct {
		Host          string
		Port          uint32
		OriginateHost string
		OriginatePort uint32
	}
	if err := ssh.Unmarshal(nch.ExtraData(), &req); err != nil {
		_ = nch.Reject(ssh.Prohibited, "malformed direct-tcpip request")
		return
	}
	c, err := net.Dial("tcp", net.JoinHostPort(req.Host, fmt.Sprint(req.Port)))
	if err != nil {
		_ = nch.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := nch.Accept()
	if err != nil {
		_ = c.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	go func() {
		defer c.Close()
		defer ch.Close()
		go func() { _, _ = io.Copy(c, ch) }()
		_, _ = io.Copy(ch, c)
	}()
}

func openPTY() (*os.File, string, error) {
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, "", err
	}
	n, err := unix.IoctlGetInt(int(ptmx.Fd()), unix.TIOCGPTN)
	if err != nil {
		_ = ptmx.Close()
		return nil, "", err
	}
	if err := unix.IoctlSetPointerInt(int(ptmx.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		_ = ptmx.Close()
		return nil, "", err
	}
	return ptmx, fmt.Sprintf("/dev/pts/%d", n), nil
}

func setWinsize(ptmx *os.File, cols, rows, width, height uint32) {
	_ = unix.IoctlSetWinsize(int(ptmx.Fd()), unix.TIOCSWINSZ, &unix.Winsize{
		Row: uint16(rows), Col: uint16(cols), Xpixel: uint16(width), Ypixel: uint16(height),
	})
}

// authorizedKeys reads the keys dclient injected when it built the rootfs:
// root's authorized_keys, plus a file of dinit's own for images where root's home
// is managed by something else.
func authorizedKeys() ([]ssh.PublicKey, error) {
	var keys []ssh.PublicKey
	paths := []string{filepath.Join(StateDir, authKeysFile)}
	if u, ok := lookupUser("root"); ok {
		paths = append(paths, filepath.Join(u.home, ".ssh", authKeysFile))
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for len(b) > 0 {
			key, _, _, rest, err := ssh.ParseAuthorizedKey(b)
			if err != nil {
				break
			}
			keys = append(keys, key)
			b = rest
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no authorized keys in %s; not starting the ssh server", strings.Join(paths, " or "))
	}
	return keys, nil
}

func hostKey() (ssh.Signer, error) {
	path := filepath.Join(StateDir, hostKeyFile)
	if b, err := os.ReadFile(path); err == nil {
		if signer, err := ssh.ParsePrivateKey(b); err == nil {
			return signer, nil
		}
		log.Printf("ssh: %s is unusable; generating a new host key", path)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(StateDir, 0o700); err != nil {
		return nil, err
	}
	body := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, body, 0o600); err != nil {
		log.Printf("ssh: could not persist the host key (%v); it will change on the next boot", err)
	}
	return ssh.NewSignerFromKey(priv)
}
