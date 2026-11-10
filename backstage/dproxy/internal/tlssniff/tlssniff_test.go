package tlssniff

import (
	"bytes"
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"
)

func helloFrom(t *testing.T, serverName string) ([]byte, string, error) {
	t.Helper()
	client, server := net.Pipe()
	defer server.Close()
	go func() {
		c := tls.Client(client, &tls.Config{ServerName: serverName, InsecureSkipVerify: true})
		_ = c.SetDeadline(time.Now().Add(2 * time.Second))
		_ = c.Handshake()
		_ = client.Close()
	}()
	_ = server.SetDeadline(time.Now().Add(2 * time.Second))
	return ReadClientHello(server, 0)
}

func TestReadClientHelloSNI(t *testing.T) {
	raw, sni, err := helloFrom(t, "One.vm.local")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if sni != "one.vm.local" {
		t.Fatalf("sni = %q", sni)
	}
	if len(raw) < 5 || raw[0] != recordHandshake {
		t.Fatalf("raw does not start with a handshake record")
	}
}

func TestReadClientHelloNoSNI(t *testing.T) {
	_, sni, err := helloFrom(t, "10.0.0.1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if sni != "" {
		t.Fatalf("an ip literal is not sent as sni, got %q", sni)
	}
}

func TestReadClientHelloSplitRecords(t *testing.T) {
	raw, want, err := helloFrom(t, "split.vm.local")
	if err != nil {
		t.Fatal(err)
	}
	hs := raw[5:]
	half := len(hs) / 2
	var split []byte
	for _, part := range [][]byte{hs[:half], hs[half:]} {
		split = append(split, recordHandshake, 0x03, 0x01, byte(len(part)>>8), byte(len(part)))
		split = append(split, part...)
	}
	got, sni, err := ReadClientHello(bytes.NewReader(split), 0)
	if err != nil {
		t.Fatalf("read split: %v", err)
	}
	if sni != want || !bytes.Equal(got, split) {
		t.Fatalf("sni = %q, replay equal = %t", sni, bytes.Equal(got, split))
	}
}

func TestReadClientHelloRejects(t *testing.T) {
	_, _, err := ReadClientHello(bytes.NewReader([]byte("GET / HTTP/1.1\r\n\r\n")), 0)
	if !errors.Is(err, ErrNotTLS) {
		t.Fatalf("plaintext http: err = %v", err)
	}
	big := []byte{recordHandshake, 0x03, 0x01, 0xff, 0xff}
	if _, _, err := ReadClientHello(bytes.NewReader(big), 1024); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized record: err = %v", err)
	}
}
