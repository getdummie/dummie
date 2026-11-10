package tlssniff

import (
	"encoding/binary"
	"errors"
	"io"
	"strings"
)

var (
	ErrNotTLS    = errors.New("tlssniff: not a tls handshake")
	ErrTooLarge  = errors.New("tlssniff: client hello too large")
	ErrMalformed = errors.New("tlssniff: malformed client hello")
)

const (
	DefaultMaxBytes = 16384

	recordHandshake    = 0x16
	handshakeHello     = 0x01
	extServerName      = 0x0000
	serverNameHostName = 0x00
)

// ReadClientHello returns every byte read, for replay to the backend, and the
// SNI host name, or "" when the hello names none.
func ReadClientHello(r io.Reader, max int) ([]byte, string, error) {
	if max <= 0 {
		max = DefaultMaxBytes
	}
	var raw, hs []byte
	hdr := make([]byte, 5)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			return raw, "", err
		}
		raw = append(raw, hdr...)
		if hdr[0] != recordHandshake {
			return raw, "", ErrNotTLS
		}
		n := int(binary.BigEndian.Uint16(hdr[3:5]))
		if n == 0 || len(raw)+n > max {
			return raw, "", ErrTooLarge
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			return raw, "", err
		}
		raw = append(raw, body...)
		hs = append(hs, body...)

		if len(hs) < 4 {
			continue
		}
		if hs[0] != handshakeHello {
			return raw, "", ErrNotTLS
		}
		size := int(hs[1])<<16 | int(hs[2])<<8 | int(hs[3])
		if 4+size > max {
			return raw, "", ErrTooLarge
		}
		if len(hs) < 4+size {
			continue
		}
		sni, err := parseHello(hs[4 : 4+size])
		return raw, sni, err
	}
}

type cursor struct {
	b   []byte
	bad bool
}

func (c *cursor) take(n int) []byte {
	if c.bad || n < 0 || n > len(c.b) {
		c.bad = true
		return nil
	}
	out := c.b[:n]
	c.b = c.b[n:]
	return out
}

func (c *cursor) u8() int {
	b := c.take(1)
	if b == nil {
		return 0
	}
	return int(b[0])
}

func (c *cursor) u16() int {
	b := c.take(2)
	if b == nil {
		return 0
	}
	return int(binary.BigEndian.Uint16(b))
}

func parseHello(body []byte) (string, error) {
	c := &cursor{b: body}
	c.take(2 + 32)
	c.take(c.u8())
	c.take(c.u16())
	c.take(c.u8())
	if c.bad {
		return "", ErrMalformed
	}
	if len(c.b) == 0 {
		return "", nil
	}
	exts := &cursor{b: c.take(c.u16())}
	if c.bad {
		return "", ErrMalformed
	}
	for len(exts.b) > 0 {
		typ := exts.u16()
		data := exts.take(exts.u16())
		if exts.bad {
			return "", ErrMalformed
		}
		if typ != extServerName {
			continue
		}
		list := &cursor{b: data}
		names := &cursor{b: list.take(list.u16())}
		for len(names.b) > 0 {
			kind := names.u8()
			name := names.take(names.u16())
			if names.bad || list.bad {
				return "", ErrMalformed
			}
			if kind == serverNameHostName {
				return strings.ToLower(strings.TrimSuffix(string(name), ".")), nil
			}
		}
		return "", nil
	}
	return "", nil
}
