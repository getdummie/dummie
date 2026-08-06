// Package xnet holds the socket plumbing shared by proxy and dpipe: adopting
// descriptors received over SCM_RIGHTS, extracting a listener's descriptor, and
// binding listeners with SO_REUSEPORT.
//
// This package is SHARED SOURCE with the proxy repository.
package xnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// FileConn adopts a connected socket descriptor and returns it as a net.Conn.
// It takes ownership of fd: the descriptor is closed before returning (net's
// FileConn works on its own dup), including on error.
func FileConn(fd int) (net.Conn, error) {
	f := os.NewFile(uintptr(fd), "adopted-conn")
	if f == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("xnet: invalid fd %d", fd)
	}
	defer f.Close()
	c, err := net.FileConn(f)
	if err != nil {
		return nil, fmt.Errorf("xnet: adopt conn: %w", err)
	}
	return c, nil
}

// FileListener adopts a listening socket descriptor. It takes ownership of fd.
func FileListener(fd int) (net.Listener, error) {
	f := os.NewFile(uintptr(fd), "adopted-listener")
	if f == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("xnet: invalid fd %d", fd)
	}
	defer f.Close()
	ln, err := net.FileListener(f)
	if err != nil {
		return nil, fmt.Errorf("xnet: adopt listener: %w", err)
	}
	return ln, nil
}

// ListenerFD returns the raw descriptor of a listener. The descriptor is not
// duplicated, so it is only valid while ln stays open — long enough to pass it
// over SCM_RIGHTS.
func ListenerFD(ln net.Listener) (int, error) {
	sc, ok := ln.(syscall.Conn)
	if !ok {
		return -1, errors.New("xnet: listener does not expose a syscall.Conn")
	}
	return connFD(sc)
}

// ConnFD returns the raw descriptor of a connection, with the same caveat as
// ListenerFD.
func ConnFD(c net.Conn) (int, error) {
	sc, ok := c.(syscall.Conn)
	if !ok {
		return -1, errors.New("xnet: conn does not expose a syscall.Conn")
	}
	return connFD(sc)
}

func connFD(sc syscall.Conn) (int, error) {
	raw, err := sc.SyscallConn()
	if err != nil {
		return -1, err
	}
	fd := -1
	if err := raw.Control(func(p uintptr) { fd = int(p) }); err != nil {
		return -1, err
	}
	if fd < 0 {
		return -1, errors.New("xnet: could not obtain descriptor")
	}
	return fd, nil
}

// WithFD runs fn with the connection's raw descriptor held live for the duration
// of the call, which is what makes an fd hand-off safe.
func WithFD(c net.Conn, fn func(fd int) error) error {
	sc, ok := c.(syscall.Conn)
	if !ok {
		return errors.New("xnet: conn does not expose a syscall.Conn")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return err
	}
	var inner error
	if err := raw.Control(func(p uintptr) { inner = fn(int(p)) }); err != nil {
		return err
	}
	return inner
}

// WithFD2 is WithFD for two connections at once (the copy hand-off).
func WithFD2(a, b net.Conn, fn func(fdA, fdB int) error) error {
	return WithFD(a, func(fa int) error {
		return WithFD(b, func(fb int) error { return fn(fa, fb) })
	})
}

// Listen binds a TCP listener, optionally with SO_REUSEPORT so that a
// replacement process can bind the same port before the old one exits.
func Listen(network, addr string, reuseport bool) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var serr error
			err := c.Control(func(fd uintptr) {
				if e := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); e != nil {
					serr = e
					return
				}
				if reuseport {
					if e := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1); e != nil {
						serr = e
					}
				}
			})
			if err != nil {
				return err
			}
			return serr
		},
	}
	return lc.Listen(context.Background(), network, addr)
}

// ListenUnix binds a unix stream listener with mode 0600. A stale socket file
// that nothing is listening on is removed first; a live one is an error.
func ListenUnix(path string) (*net.UnixListener, error) {
	if err := removeStaleSocket(path); err != nil {
		return nil, err
	}
	addr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return nil, err
	}
	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, err
	}
	ln.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

func removeStaleSocket(path string) error {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	c, err := net.Dial("unix", path)
	if err == nil {
		_ = c.Close()
		return fmt.Errorf("xnet: %s is already in use by a live process", path)
	}
	return os.Remove(path)
}
