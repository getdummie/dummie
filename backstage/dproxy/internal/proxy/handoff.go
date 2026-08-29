package proxy

import (
	"context"
	"net"

	"dproxy/internal/control"
	"dproxy/internal/xnet"
)

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

func (p *Proxy) handoffSSHAccept(id string, client net.Conn) error {
	return xnet.WithFD(client, func(fd int) error {
		return p.ctrl.SSHAccept(context.Background(), id, fd)
	})
}

// handoffSessionAccept covers both the console and the desktop: the message
// already carries the type, so the fd passing is identical.
func (p *Proxy) handoffSessionAccept(m control.Msg, client net.Conn) error {
	return xnet.WithFD(client, func(fd int) error {
		return p.ctrl.SessionAccept(context.Background(), m, fd)
	})
}

func (p *Proxy) handoffRDPAccept(id string, client net.Conn) error {
	return xnet.WithFD(client, func(fd int) error {
		return p.ctrl.RDPAccept(context.Background(), id, fd)
	})
}

func (p *Proxy) handoffTLSAccept(id string, client net.Conn) error {
	return xnet.WithFD(client, func(fd int) error {
		return p.ctrl.TLSAccept(context.Background(), id, fd)
	})
}
