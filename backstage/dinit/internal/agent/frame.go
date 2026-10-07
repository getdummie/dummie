package agent

import (
	"encoding/binary"
	"fmt"
	"io"
)

// A frame is one websocket message carried over a byte stream: one opcode byte,
// a big-endian length, then the payload. dpipe speaks the same framing.
const (
	OpText   byte = 1
	OpBinary byte = 2

	MaxFrame = 64 << 20
)

func ReadFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > MaxFrame {
		return 0, nil, fmt.Errorf("frame of %d bytes is over the %d byte limit", n, MaxFrame)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return hdr[0], buf, nil
}

func WriteFrame(w io.Writer, op byte, payload []byte) error {
	if len(payload) > MaxFrame {
		return fmt.Errorf("frame of %d bytes is over the %d byte limit", len(payload), MaxFrame)
	}
	buf := make([]byte, 5+len(payload))
	buf[0] = op
	binary.BigEndian.PutUint32(buf[1:], uint32(len(payload)))
	copy(buf[5:], payload)
	_, err := w.Write(buf)
	return err
}
