package dpipe

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"dpipe/internal/rdp"
)

// redirectFrame builds a Server Redirection PDU carrying token, shaped the way
// gnome-remote-desktop sends one. The layout is the captured frame that
// internal/rdp/redirect_test.go pins, with only the load balance info replaced.
func redirectFrame(token string) []byte {
	body := []byte{
		0x02, 0xf0, 0x80, // X.224 data
		0x68,                         // MCS send data indication
		0x00, 0x08, 0x03, 0xeb, 0x70, // initiator, channel, priority
		0x80, 0x00, // two-byte per-PDU length
		0x00, 0x00, 0x1a, 0x00, 0x00, 0x00, // share control: length, redirection, source
		0x00, 0x00, // filler ahead of the redirection packet
		0x00, 0x04, // SEC_REDIRECTION_PKT
		0x00, 0x00, // length
		0x00, 0x00, 0x00, 0x00, // session id
		0x16, 0xc0, 0x01, 0x00, // redirection flags, no target net address
	}
	body = binary.LittleEndian.AppendUint32(body, uint32(len(token)))
	body = append(body, token...)

	out := make([]byte, 4, 4+len(body))
	out[0] = 3
	binary.BigEndian.PutUint16(out[2:], uint16(4+len(body)))
	return append(out, body...)
}

func authRequestPDU(user, password string) []byte {
	b := make([]byte, 0, 64)
	b = binary.LittleEndian.AppendUint16(b, 0x0001)
	b = binary.LittleEndian.AppendUint16(b, 0x0002)
	b = binary.LittleEndian.AppendUint16(b, rdp.RDSTLSDataPasswordCreds)
	for _, f := range [][]byte{bytes.Repeat([]byte{0x7f}, 16), []byte(user), nil, []byte(password)} {
		b = binary.LittleEndian.AppendUint16(b, uint16(len(f)))
		b = append(b, f...)
	}
	return b
}

// fakeGuest answers one handover the way gnome-remote-desktop does: RDSTLS,
// then a redirection to the next session. It reports the authentication request
// it received so the test can check the proxy forwarded it untouched.
func fakeGuest(t *testing.T, wantCookie, nextToken string) (addr string, got chan []byte) {
	t.Helper()
	crt, _, _, err := generateSelfSigned("guest")
	if err != nil {
		t.Fatalf("guest certificate: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	got = make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(10 * time.Second))

		req, err := rdp.ReadConnectionRequest(c)
		if err != nil {
			t.Errorf("guest: connection request: %v", err)
			return
		}
		if req.Cookie != wantCookie {
			t.Errorf("guest: cookie = %q, want %q", req.Cookie, wantCookie)
			return
		}
		if req.RequestedProtocols&rdp.ProtocolRDSTLS == 0 {
			t.Errorf("guest: protocols = %d, want RDSTLS", req.RequestedProtocols)
			return
		}
		if err := rdp.WriteConnectionConfirm(c, req.SrcRef, 0x03, rdp.ProtocolRDSTLS); err != nil {
			t.Errorf("guest: confirm: %v", err)
			return
		}

		gtls := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{*crt}})
		if err := gtls.Handshake(); err != nil {
			t.Errorf("guest: tls: %v", err)
			return
		}
		if err := rdp.WriteRDSTLSCapabilities(gtls); err != nil {
			t.Errorf("guest: capabilities: %v", err)
			return
		}
		auth, err := rdp.ReadRDSTLSAuthRequest(gtls)
		if err != nil {
			t.Errorf("guest: authentication request: %v", err)
			return
		}
		got <- auth.Raw
		if err := rdp.WriteRDSTLSAuthResponse(gtls, rdp.RDSTLSSuccess); err != nil {
			t.Errorf("guest: authentication response: %v", err)
			return
		}
		if _, err := gtls.Write(redirectFrame(nextToken)); err != nil {
			t.Errorf("guest: redirect: %v", err)
			return
		}
		// Hold the connection open so the proxy does not tear the leg down
		// before the client has read the redirection.
		var sink [256]byte
		_, _ = gtls.Read(sink[:])
	}()
	return ln.Addr().String(), got
}

func rdpTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := baseConfig(t)
	dir := t.TempDir()
	cfg.RDP = RDPConfig{
		Enabled:        true,
		SelfSignedCert: dir + "/rdp.pem",
		SelfSignedKey:  dir + "/rdp.key",
	}
	s, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// The second handover is the one that used to be lost: with the reconnect leg
// spliced raw, its redirection PDU was inside client-to-guest TLS and the proxy
// never saw the token. Terminating RDSTLS makes it visible again.
func TestRDPHandoverTerminatesRDSTLSAndSeesTheNextRedirect(t *testing.T) {
	const (
		token1 = "Cookie: msts=1704979740"
		token2 = "Cookie: msts=1704979999\r\n"
	)
	s := rdpTestServer(t)
	guest, guestAuth := fakeGuest(t, token1, token2)
	s.handovers.put([]byte(token1), guest)

	client, proxy := net.Pipe()
	defer client.Close()
	go s.serveRDP(nil, "test-handover", proxy)

	_ = client.SetDeadline(time.Now().Add(10 * time.Second))
	if err := rdp.WriteConnectionRequest(client, token1, rdp.ProtocolRDSTLS); err != nil {
		t.Fatalf("connection request: %v", err)
	}
	selected, err := rdp.ReadConnectionConfirm(client)
	if err != nil {
		t.Fatalf("connection confirm: %v", err)
	}
	if selected != rdp.ProtocolRDSTLS {
		t.Fatalf("proxy selected protocol %d, want RDSTLS", selected)
	}

	ctls := tls.Client(client, &tls.Config{InsecureSkipVerify: true})
	if err := ctls.Handshake(); err != nil {
		t.Fatalf("client tls: %v", err)
	}
	if err := rdp.ReadRDSTLSCapabilities(ctls); err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	sent := authRequestPDU("ubuntu", "one-time")
	if _, err := ctls.Write(sent); err != nil {
		t.Fatalf("authentication request: %v", err)
	}
	code, err := rdp.ReadRDSTLSAuthResponse(ctls)
	if err != nil {
		t.Fatalf("authentication response: %v", err)
	}
	if code != rdp.RDSTLSSuccess {
		t.Fatalf("result code = 0x%08x", code)
	}

	select {
	case raw := <-guestAuth:
		if !bytes.Equal(raw, sent) {
			t.Fatalf("guest saw %x, client sent %x", raw, sent)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("guest never received the authentication request")
	}

	// The redirection has to reach the client unchanged as well as being seen.
	want := redirectFrame(token2)
	got := make([]byte, len(want))
	if _, err := io.ReadFull(ctls, got); err != nil {
		t.Fatalf("read redirect: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("redirect frame altered: %x", got)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if target, ok := s.handovers.take(token2); ok {
			if target != guest {
				t.Fatalf("second handover target = %q, want %q", target, guest)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the second redirection was not registered as a handover")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
