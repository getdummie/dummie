package httpsniff_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"dpipe/internal/httpsniff"
)

func TestHeaderOnly(t *testing.T) {
	req := "GET /index.html HTTP/1.1\r\nHost: VM1.local:8443\r\nUser-Agent: x\r\n\r\n"
	buf, host, err := httpsniff.ReadHeaderBlock(strings.NewReader(req), 65536)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if host != "vm1.local" {
		t.Fatalf("host = %q", host)
	}
	if string(buf) != req {
		t.Fatalf("prefix mismatch: %q", buf)
	}
}

func TestHeaderPlusPartialBodyIsNotLost(t *testing.T) {
	req := "POST /submit HTTP/1.1\r\nHost: vm2.local\r\nContent-Length: 5\r\n\r\nhello"
	buf, host, err := httpsniff.ReadHeaderBlock(strings.NewReader(req), 65536)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if host != "vm2.local" {
		t.Fatalf("host = %q", host)
	}
	if string(buf) != req {
		t.Fatalf("body bytes lost: %q", buf)
	}
}

func TestAbsoluteFormURI(t *testing.T) {
	req := "GET http://user@vm3.local:8080/path?q=1 HTTP/1.1\r\nHost: ignored.local\r\n\r\n"
	_, host, err := httpsniff.ReadHeaderBlock(strings.NewReader(req), 65536)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if host != "vm3.local" {
		t.Fatalf("host = %q", host)
	}
}

func TestMissingHost(t *testing.T) {
	req := "GET / HTTP/1.1\r\nUser-Agent: x\r\n\r\n"
	_, _, err := httpsniff.ReadHeaderBlock(strings.NewReader(req), 65536)
	if !errors.Is(err, httpsniff.ErrNoHost) {
		t.Fatalf("err = %v, want ErrNoHost", err)
	}
}

func TestMalformedRequestLine(t *testing.T) {
	req := "NOT-HTTP\r\nHost: vm1.local\r\n\r\n"
	_, _, err := httpsniff.ReadHeaderBlock(strings.NewReader(req), 65536)
	if !errors.Is(err, httpsniff.ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed", err)
	}
}

func TestTruncatedRequest(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: vm1.local\r\n"
	_, _, err := httpsniff.ReadHeaderBlock(strings.NewReader(req), 65536)
	if err == nil {
		t.Fatal("expected an error for a request without a header terminator")
	}
}

func TestOversize(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: vm1.local\r\nX-Pad: " + strings.Repeat("p", 4096) + "\r\n\r\n"
	_, _, err := httpsniff.ReadHeaderBlock(strings.NewReader(req), 1024)
	if !errors.Is(err, httpsniff.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestTerminatorSplitAcrossReads(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: vm1.local\r\n\r\nbody"
	_, host, err := httpsniff.ReadHeaderBlock(&oneByteReader{s: req}, 65536)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if host != "vm1.local" {
		t.Fatalf("host = %q", host)
	}
}

type oneByteReader struct {
	s string
	i int
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if r.i >= len(r.s) {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = r.s[r.i]
	r.i++
	return 1, nil
}
