package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"golang.org/x/sys/unix"
)

// ErrTooLarge is returned when a message exceeds MaxMsgSize.
var ErrTooLarge = errors.New("control: message too large")

// marshal encodes m and enforces the size limit.
func marshal(m Msg) ([]byte, error) {
	if m.V == 0 {
		m.V = Version
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxMsgSize {
		return nil, ErrTooLarge
	}
	return b, nil
}

// SendMsg writes one message (and optionally its file descriptors) with a single
// sendmsg(2). A successful return means the kernel has duplicated the fds into
// the peer process: the caller still owns its own copies and should close them.
func SendMsg(c *net.UnixConn, m Msg, fds []int) error {
	b, err := marshal(m)
	if err != nil {
		return err
	}
	var oob []byte
	if len(fds) > 0 {
		oob = unix.UnixRights(fds...)
	}
	n, _, err := c.WriteMsgUnix(b, oob, nil)
	if err != nil {
		return err
	}
	for n < len(b) { // stream socket: finish a short write (fds already sent)
		w, err := c.Write(b[n:])
		if err != nil {
			return err
		}
		n += w
	}
	return nil
}

// RecvMsg reads exactly one message from c. It is only safe on connections where
// messages strictly alternate (the upgrade handshake); for duplex traffic use
// Conn, which tolerates stream coalescing.
func RecvMsg(c *net.UnixConn) (Msg, []int, error) {
	return NewConn(c).RecvMsg()
}

// Conn is a framed, buffered view of a control connection.
//
// AF_UNIX stream sockets preserve the boundary of any segment that carries
// SCM_RIGHTS, but two fd-less messages may coalesce into one recvmsg. Conn
// therefore keeps a read buffer and decodes one JSON object at a time, which
// keeps the wire format exactly as specified (bare JSON objects, no length
// prefix) while remaining correct under coalescing.
type Conn struct {
	c *net.UnixConn

	wmu sync.Mutex

	rbuf []byte
}

// NewConn wraps a unix connection.
func NewConn(c *net.UnixConn) *Conn { return &Conn{c: c} }

// Unix returns the underlying connection.
func (k *Conn) Unix() *net.UnixConn { return k.c }

// Close closes the underlying connection.
func (k *Conn) Close() error { return k.c.Close() }

// SendMsg is safe for concurrent use.
func (k *Conn) SendMsg(m Msg, fds []int) error {
	k.wmu.Lock()
	defer k.wmu.Unlock()
	return SendMsg(k.c, m, fds)
}

// RecvMsg returns the next message and any file descriptors that accompanied it.
// It must be called from a single goroutine.
func (k *Conn) RecvMsg() (Msg, []int, error) {
	for {
		if m, ok, err := k.decodeBuffered(); err != nil {
			return Msg{}, nil, err
		} else if ok {
			return m, nil, nil
		}

		data := make([]byte, MaxMsgSize)
		oob := make([]byte, unix.CmsgSpace(4*MaxRecvFDs))
		n, oobn, flags, _, err := k.c.ReadMsgUnix(data, oob)
		// A failed recvmsg(2) reports its raw -1 return through n (and possibly
		// oobn): internal/poll passes both through unchanged when the syscall
		// errors. Clamp before either value is used as a slice bound.
		if n < 0 {
			n = 0
		}
		if oobn < 0 {
			oobn = 0
		}
		if n == 0 && oobn == 0 {
			if err == nil {
				err = io.EOF
			}
			return Msg{}, nil, err
		}

		fds, ferr := parseRights(oob[:oobn])
		if flags&unix.MSG_CTRUNC != 0 {
			closeFDs(fds)
			return Msg{}, nil, errors.New("control: truncated ancillary data")
		}
		if ferr != nil {
			closeFDs(fds)
			return Msg{}, nil, ferr
		}

		k.rbuf = append(k.rbuf, data[:n]...)

		m, ok, derr := k.decodeBuffered()
		if derr != nil {
			closeFDs(fds)
			return Msg{}, nil, derr
		}
		if !ok {
			if len(fds) > 0 {
				closeFDs(fds)
				return Msg{}, nil, errors.New("control: fds arrived with an incomplete message")
			}
			if err != nil {
				return Msg{}, nil, err
			}
			continue
		}
		return m, fds, nil
	}
}

// decodeBuffered pops the first complete JSON object from the read buffer.
func (k *Conn) decodeBuffered() (Msg, bool, error) {
	trimLeadingSpace(&k.rbuf)
	if len(k.rbuf) == 0 {
		return Msg{}, false, nil
	}
	dec := json.NewDecoder(bytes.NewReader(k.rbuf))
	var m Msg
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			if len(k.rbuf) > MaxMsgSize {
				return Msg{}, false, ErrTooLarge
			}
			return Msg{}, false, nil
		}
		return Msg{}, false, fmt.Errorf("control: malformed message: %w", err)
	}
	k.rbuf = k.rbuf[dec.InputOffset():]
	if m.V != Version {
		return Msg{}, false, fmt.Errorf("control: unsupported version %d", m.V)
	}
	return m, true, nil
}

func trimLeadingSpace(b *[]byte) {
	i := 0
	for i < len(*b) {
		switch (*b)[i] {
		case ' ', '\t', '\r', '\n':
			i++
			continue
		}
		break
	}
	*b = (*b)[i:]
}

func parseRights(oob []byte) ([]int, error) {
	if len(oob) == 0 {
		return nil, nil
	}
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, fmt.Errorf("control: parse control message: %w", err)
	}
	var out []int
	for _, m := range msgs {
		if m.Header.Level != unix.SOL_SOCKET || m.Header.Type != unix.SCM_RIGHTS {
			continue
		}
		fds, err := unix.ParseUnixRights(&m)
		if err != nil {
			closeFDs(out)
			return nil, fmt.Errorf("control: parse rights: %w", err)
		}
		out = append(out, fds...)
	}
	for _, fd := range out {
		unix.CloseOnExec(fd)
	}
	if len(out) > MaxRecvFDs {
		closeFDs(out)
		return nil, errors.New("control: too many file descriptors")
	}
	return out, nil
}

// CloseFDs closes a set of received descriptors.
func CloseFDs(fds []int) { closeFDs(fds) }

func closeFDs(fds []int) {
	for _, fd := range fds {
		_ = unix.Close(fd)
	}
}
