package dpipe

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

const (
	wsCloseNormal        = 1000
	wsCloseProtocolError = 1002
	wsCloseTooLarge      = 1009
	wsCloseInternalError = 1011
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const maxWSMessage = 1 << 20

const wsCloseTimeout = 2 * time.Second

var errWSClosed = errors.New("ws: peer closed")

type wsConn struct {
	c  net.Conn
	br *bufio.Reader

	wmu       sync.Mutex
	closeOnce sync.Once
}

func wsAcceptKey(key string) string {
	h := sha1.New()
	_, _ = io.WriteString(h, key+wsGUID)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func wsUpgrade(c net.Conn, key string, pipelined []byte) (*wsConn, error) {
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + wsAcceptKey(key) + "\r\n\r\n"
	if _, err := io.WriteString(c, resp); err != nil {
		return nil, fmt.Errorf("ws: write handshake: %w", err)
	}
	var r io.Reader = c
	if len(pipelined) > 0 {
		r = io.MultiReader(bytes.NewReader(pipelined), c)
	}
	return &wsConn{c: c, br: bufio.NewReader(r)}, nil
}

func (w *wsConn) ReadMessage() (opcode byte, payload []byte, err error) {
	var msgOp byte
	var buf []byte
	for {
		fin, op, data, err := w.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case opPing:
			if err := w.WriteMessage(opPong, data); err != nil {
				return 0, nil, err
			}
			continue
		case opPong:
			continue
		case opClose:
			return 0, nil, errWSClosed
		case opText, opBinary:
			if msgOp != 0 {
				w.fail(wsCloseProtocolError, "interleaved message")
				return 0, nil, errors.New("ws: new message before the previous one finished")
			}
			msgOp, buf = op, data
		case opContinuation:
			if msgOp == 0 {
				w.fail(wsCloseProtocolError, "unexpected continuation")
				return 0, nil, errors.New("ws: continuation without a start frame")
			}
			buf = append(buf, data...)
		default:
			w.fail(wsCloseProtocolError, "unknown opcode")
			return 0, nil, fmt.Errorf("ws: unknown opcode %#x", op)
		}
		if len(buf) > maxWSMessage {
			w.fail(wsCloseTooLarge, "message too large")
			return 0, nil, errors.New("ws: message too large")
		}
		if fin {
			return msgOp, buf, nil
		}
	}
}

func (w *wsConn) readFrame() (fin bool, opcode byte, payload []byte, err error) {
	var head [2]byte
	if _, err := io.ReadFull(w.br, head[:]); err != nil {
		return false, 0, nil, err
	}
	fin = head[0]&0x80 != 0
	if head[0]&0x70 != 0 {
		w.fail(wsCloseProtocolError, "reserved bits set")
		return false, 0, nil, errors.New("ws: reserved bits set")
	}
	opcode = head[0] & 0x0F
	masked := head[1]&0x80 != 0
	length := uint64(head[1] & 0x7F)

	isControl := opcode&0x08 != 0
	if isControl && (length > 125 || !fin) {
		w.fail(wsCloseProtocolError, "bad control frame")
		return false, 0, nil, errors.New("ws: invalid control frame")
	}

	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(w.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(w.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	if !masked {
		w.fail(wsCloseProtocolError, "unmasked client frame")
		return false, 0, nil, errors.New("ws: client frame is not masked")
	}
	if length > maxWSMessage {
		w.fail(wsCloseTooLarge, "frame too large")
		return false, 0, nil, errors.New("ws: frame too large")
	}

	var mask [4]byte
	if _, err := io.ReadFull(w.br, mask[:]); err != nil {
		return false, 0, nil, err
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(w.br, payload); err != nil {
		return false, 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return fin, opcode, payload, nil
}

func (w *wsConn) WriteMessage(opcode byte, payload []byte) error {
	head := make([]byte, 0, 10)
	head = append(head, 0x80|opcode)
	switch n := len(payload); {
	case n <= 125:
		head = append(head, byte(n))
	case n <= 0xFFFF:
		head = append(head, 126, byte(n>>8), byte(n))
	default:
		head = append(head, 127)
		head = binary.BigEndian.AppendUint64(head, uint64(n))
	}

	w.wmu.Lock()
	defer w.wmu.Unlock()
	if _, err := w.c.Write(append(head, payload...)); err != nil {
		return err
	}
	return nil
}

func (w *wsConn) WriteClose(code uint16, reason string) error {
	if len(reason) > 123 {
		reason = reason[:123]
	}
	body := binary.BigEndian.AppendUint16(nil, code)
	return w.WriteMessage(opClose, append(body, reason...))
}

func (w *wsConn) Close(code uint16, reason string) {
	w.closeOnce.Do(func() {
		_ = w.c.SetWriteDeadline(time.Now().Add(wsCloseTimeout))
		_ = w.WriteClose(code, reason)
		_ = w.c.Close()
	})
}

func (w *wsConn) fail(code uint16, reason string) { w.Close(code, reason) }
