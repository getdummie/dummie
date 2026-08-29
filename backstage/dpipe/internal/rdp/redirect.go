package rdp

import "encoding/binary"

// Server Redirection detection on the post-NLA server-to-client stream.
//
// gnome-remote-desktop's system mode authenticates at the login screen and then
// hands the client to the user session with a Server Redirection PDU carrying
// one-time credentials. The client reconnects to whoever it was already talking
// to — the proxy — replaying the PDU's load balance info as an X.224 routing
// token, in the clear. Recognising that token is what lets the proxy send the
// second half of the handover to the same guest without terminating RDSTLS.
//
// Only the load balance info is extracted. The credentials stay opaque.

const (
	secRedirectionPkt = 0x0400

	pduTypeServerRedirection = 0x0A

	lbTargetNetAddress = 0x00000001
	lbLoadBalanceInfo  = 0x00000002

	mcsSendDataIndication = 26

	// MaxRedirectPDU bounds a redirection PDU. The target certificate makes
	// these a few KB; anything larger is not one.
	MaxRedirectPDU = 16384
)

// RedirectScanner watches a server-to-client stream for the redirection PDU.
// Feed it every byte in that direction; it reports the load balance info once
// and goes inert.
type RedirectScanner struct {
	buf  []byte
	done bool
}

// Scan consumes stream bytes and returns the load balance info the first time a
// Server Redirection PDU completes, or nil. The bytes must still be forwarded
// verbatim: this only inspects.
func (s *RedirectScanner) Scan(p []byte) []byte {
	if s.done {
		return nil
	}
	s.buf = append(s.buf, p...)

	for {
		n, ok := frameLen(s.buf)
		if !ok {
			// A frame header that cannot ever be valid means the stream is not
			// what this scanner assumes; stop rather than resync on noise.
			if len(s.buf) > MaxRedirectPDU {
				s.done = true
				s.buf = nil
			}
			return nil
		}
		if n < 2 || n > MaxRedirectPDU {
			s.done = true
			s.buf = nil
			return nil
		}
		if len(s.buf) < n {
			return nil
		}
		frame := s.buf[:n]
		s.buf = s.buf[n:]

		if frame[0] == tpktVersion {
			if info := redirectLoadBalanceInfo(frame[tpktHeader:]); info != nil {
				s.done = true
				s.buf = nil
				return info
			}
		}
	}
}

// frameLen returns the length of the leading PDU, covering both TPKT and the
// fast-path output header that carries most server traffic once the session is
// active.
func frameLen(b []byte) (int, bool) {
	if len(b) < 2 {
		return 0, false
	}
	if b[0] == tpktVersion {
		if len(b) < tpktHeader {
			return 0, false
		}
		return int(binary.BigEndian.Uint16(b[2:])), true
	}
	if b[1]&0x80 != 0 {
		if len(b) < 3 {
			return 0, false
		}
		return int(b[1]&0x7f)<<8 | int(b[2]), true
	}
	return int(b[1]), true
}

// redirectLoadBalanceInfo parses a TPKT payload and returns the redirection
// PDU's load balance info, or nil if the payload is anything else.
func redirectLoadBalanceInfo(body []byte) []byte {
	c := cursor{b: body}

	// X.224 data TPDU: li, code, eot.
	if c.byteAt(0) != 2 || c.byteAt(1) != 0xF0 {
		return nil
	}
	c.skip(3)

	// MCS SendDataIndication: choice, initiator, channelId, priority, length.
	choice, ok := c.next()
	if !ok || choice>>2 != mcsSendDataIndication {
		return nil
	}
	c.skip(5)
	l, ok := c.next()
	if !ok {
		return nil
	}
	if l&0x80 != 0 {
		c.skip(1)
	}

	// Share control header: totalLength, pduType, pduSource.
	c.skip(2)
	pduType, ok := c.uint16()
	if !ok || pduType&0x0F != pduTypeServerRedirection {
		return nil
	}
	c.skip(2)

	// Two bytes sit between the share control header and the redirection packet
	// on the wire; the packet then opens with the flags word rather than being
	// preceded by a security header of its own.
	c.skip(2)
	flags, ok := c.uint16()
	if !ok || flags != secRedirectionPkt {
		return nil
	}

	// Length, SessionID, then the flags naming which optional fields follow.
	c.skip(2)
	c.skip(4)
	redirFlags, ok := c.uint32()
	if !ok {
		return nil
	}
	if redirFlags&lbLoadBalanceInfo == 0 {
		return nil
	}
	// Optional fields appear in flag order, each length-prefixed.
	if redirFlags&lbTargetNetAddress != 0 {
		if !c.skipField() {
			return nil
		}
	}
	return c.field()
}

type cursor struct {
	b   []byte
	pos int
}

func (c *cursor) byteAt(i int) byte {
	if c.pos+i >= len(c.b) {
		return 0
	}
	return c.b[c.pos+i]
}

func (c *cursor) skip(n int) { c.pos += n }

func (c *cursor) next() (byte, bool) {
	if c.pos >= len(c.b) {
		return 0, false
	}
	v := c.b[c.pos]
	c.pos++
	return v, true
}

func (c *cursor) uint16() (uint16, bool) {
	if c.pos+2 > len(c.b) {
		return 0, false
	}
	v := binary.LittleEndian.Uint16(c.b[c.pos:])
	c.pos += 2
	return v, true
}

func (c *cursor) uint32() (uint32, bool) {
	if c.pos+4 > len(c.b) {
		return 0, false
	}
	v := binary.LittleEndian.Uint32(c.b[c.pos:])
	c.pos += 4
	return v, true
}

// field reads a length-prefixed optional field of the redirection PDU.
func (c *cursor) field() []byte {
	n, ok := c.uint32()
	if !ok || int(n) > len(c.b)-c.pos {
		return nil
	}
	v := c.b[c.pos : c.pos+int(n)]
	c.pos += int(n)
	return v
}

func (c *cursor) skipField() bool { return c.field() != nil }
