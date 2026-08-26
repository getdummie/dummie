package dpipe

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
)

// wsPair returns a wsConn reading from what a client writes, plus the buffer the
// server's own frames land in.
func wsPair(t *testing.T, clientBytes []byte) (*wsConn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	done := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- nil
			return
		}
		done <- c
	}()
	cc, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	sc := <-done
	if sc == nil {
		t.Fatal("accept failed")
	}
	t.Cleanup(func() { _ = cc.Close(); _ = sc.Close() })

	if len(clientBytes) > 0 {
		if _, err := cc.Write(clientBytes); err != nil {
			t.Fatalf("write client bytes: %v", err)
		}
	}
	return &wsConn{c: sc, br: bufio.NewReader(sc)}, cc
}

// nopConn is a net.Conn that only supports writing, for framing assertions.
type nopConn struct {
	net.Conn
	w io.Writer
}

func (n nopConn) Write(p []byte) (int, error) { return n.w.Write(p) }
func (nopConn) Close() error                  { return nil }

// maskedFrame builds a client frame: FIN as given, masked, fixed mask key.
func maskedFrame(fin bool, opcode byte, payload []byte) []byte {
	var head byte
	if fin {
		head = 0x80
	}
	out := []byte{head | opcode}
	mask := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	switch n := len(payload); {
	case n <= 125:
		out = append(out, 0x80|byte(n))
	case n <= 0xFFFF:
		out = append(out, 0x80|126, byte(n>>8), byte(n))
	default:
		out = append(out, 0x80|127)
		out = binary.BigEndian.AppendUint64(out, uint64(n))
	}
	out = append(out, mask...)
	for i, b := range payload {
		out = append(out, b^mask[i%4])
	}
	return out
}

// RFC 6455 §1.3 worked example.
func TestWSAcceptKey(t *testing.T) {
	if got := wsAcceptKey("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("wsAcceptKey = %q", got)
	}
}

func TestWSUpgradeResponseAndPipelinedBytes(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })

	type result struct {
		ws  *wsConn
		err error
	}
	res := make(chan result, 1)
	go func() {
		ws, err := wsUpgrade(server, "dGhlIHNhbXBsZSBub25jZQ==", maskedFrame(true, opBinary, []byte("early")))
		res <- result{ws, err}
	}()

	head := make([]byte, 512)
	n, err := client.Read(head)
	if err != nil {
		t.Fatalf("read handshake: %v", err)
	}
	got := string(head[:n])
	for _, want := range []string{"HTTP/1.1 101 Switching Protocols",
		"Upgrade: websocket", "Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo="} {
		if !strings.Contains(got, want) {
			t.Errorf("handshake response missing %q:\n%s", want, got)
		}
	}

	r := <-res
	if r.err != nil {
		t.Fatalf("wsUpgrade: %v", r.err)
	}
	// Bytes the client pipelined behind the request must not be lost.
	op, payload, err := r.ws.ReadMessage()
	if err != nil || op != opBinary || string(payload) != "early" {
		t.Fatalf("ReadMessage = (%#x, %q, %v)", op, payload, err)
	}
}

func TestWSReadFragmentedMessage(t *testing.T) {
	frames := append(maskedFrame(false, opText, []byte(`{"type":`)),
		maskedFrame(true, opContinuation, []byte(`"resize"}`))...)
	ws, _ := wsPair(t, frames)

	op, payload, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if op != opText || string(payload) != `{"type":"resize"}` {
		t.Fatalf("ReadMessage = (%#x, %q)", op, payload)
	}
}

func TestWSAnswersPingBeforeData(t *testing.T) {
	frames := append(maskedFrame(true, opPing, []byte("hi")),
		maskedFrame(true, opBinary, []byte("ls\r"))...)
	ws, client := wsPair(t, frames)

	op, payload, err := ws.ReadMessage()
	if err != nil || op != opBinary || string(payload) != "ls\r" {
		t.Fatalf("ReadMessage = (%#x, %q, %v)", op, payload, err)
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(client, head); err != nil {
		t.Fatalf("read pong: %v", err)
	}
	if head[0] != 0x80|opPong || head[1] != 2 || string(head[2:4]) != "hi" {
		t.Fatalf("expected a pong echoing the payload, got %#v", head)
	}
}

func TestWSReadRejectsUnmaskedClientFrame(t *testing.T) {
	// Same frame as maskedFrame but with the mask bit and key removed.
	ws, _ := wsPair(t, []byte{0x80 | opBinary, 2, 'h', 'i'})
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Fatal("an unmasked client frame must be rejected")
	}
}

func TestWSReadRejectsCloseWithErrWSClosed(t *testing.T) {
	ws, _ := wsPair(t, maskedFrame(true, opClose, []byte{0x03, 0xE8}))
	if _, _, err := ws.ReadMessage(); !errors.Is(err, errWSClosed) {
		t.Fatalf("ReadMessage err = %v, want errWSClosed", err)
	}
}

func TestWSWriteMessageFraming(t *testing.T) {
	var buf bytes.Buffer
	ws := &wsConn{c: nopConn{w: &buf}}

	if err := ws.WriteMessage(opBinary, bytes.Repeat([]byte("x"), 200)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	out := buf.Bytes()
	// FIN+binary, no mask bit, 126 + 16-bit length.
	if out[0] != 0x80|opBinary || out[1] != 126 || binary.BigEndian.Uint16(out[2:4]) != 200 {
		t.Fatalf("unexpected header %#v", out[:4])
	}
	if len(out) != 4+200 {
		t.Fatalf("frame length = %d, want %d", len(out), 4+200)
	}
}

func TestWSWriteCloseCarriesCodeAndReason(t *testing.T) {
	var buf bytes.Buffer
	ws := &wsConn{c: nopConn{w: &buf}}

	if err := ws.WriteClose(wsCloseNormal, "session ended"); err != nil {
		t.Fatalf("WriteClose: %v", err)
	}
	out := buf.Bytes()
	if out[0] != 0x80|opClose {
		t.Fatalf("opcode = %#x", out[0])
	}
	if binary.BigEndian.Uint16(out[2:4]) != wsCloseNormal || string(out[4:]) != "session ended" {
		t.Fatalf("close payload = %#v", out[2:])
	}
}
