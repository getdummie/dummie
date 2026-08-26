package proxy

import (
	"context"
	"net"

	"dproxy/internal/control"
	"dproxy/internal/xnet"
)

// handoffCopy passes both descriptors to dpipe in one sendmsg while they are
// held live by SyscallConn().Control (no dup, no blocking-mode change). The
// kernel duplicates them into dpipe; the proxy then closes its own copies so
// dpipe is the sole owner.
func (p *Proxy) handoffCopy(id string, client, backend net.Conn, protocol string) {
	err := xnet.WithFD2(client, backend, func(cfd, bfd int) error {
		return p.ctrl.Copy(context.Background(), id, protocol, cfd, bfd)
	})
	_ = client.Close()
	_ = backend.Close()
	if err != nil {
		p.log.Warn("copy handoff failed", "id", id, "protocol", protocol, "err", err)
		return
	}
	p.log.Debug("copy handed off", "id", id, "protocol", protocol)
}

// handoffSSHAccept passes the raw pre-SSH socket to dpipe. The caller closes
// the connection afterwards either way.
func (p *Proxy) handoffSSHAccept(id string, client net.Conn) error {
	return xnet.WithFD(client, func(fd int) error {
		return p.ctrl.SSHAccept(context.Background(), id, fd)
	})
}

// handoffConsoleAccept passes the browser's socket to dpipe along with
// everything it needs to answer the upgrade and open the shell. The caller
// closes the connection afterwards either way.
func (p *Proxy) handoffConsoleAccept(m control.Msg, client net.Conn) error {
	return xnet.WithFD(client, func(fd int) error {
		return p.ctrl.ConsoleAccept(context.Background(), m, fd)
	})
}

// handoffTLSAccept passes the raw pre-TLS socket to dpipe.
func (p *Proxy) handoffTLSAccept(id string, client net.Conn) error {
	return xnet.WithFD(client, func(fd int) error {
		return p.ctrl.TLSAccept(context.Background(), id, fd)
	})
}
