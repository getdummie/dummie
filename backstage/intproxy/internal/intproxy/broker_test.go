package intproxy

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeBroker serves the token endpoint on a unix socket, the same way dclient
// does on a host.
func fakeBroker(t *testing.T, h http.HandlerFunc) *broker {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "broker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	cfg := testConfig()
	cfg.Broker.Socket = sock
	return newBroker(cfg)
}

func gitReq() request {
	return request{kind: kindGit, owner: "getdummie", repo: "dummie"}
}

func TestBrokerCachesUntilSkew(t *testing.T) {
	var calls atomic.Int64
	b := fakeBroker(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(tokenResponse{
			Token:     "ghs_one",
			ExpiresAt: time.Now().Add(time.Hour),
		})
	})

	for range 3 {
		tok, err := b.token(context.Background(), "10.64.0.7", gitReq())
		if err != nil {
			t.Fatal(err)
		}
		if tok != "ghs_one" {
			t.Fatalf("token = %q", tok)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("broker called %d times, want 1", n)
	}
}

func TestBrokerRefetchesInsideSkew(t *testing.T) {
	var calls atomic.Int64
	b := fakeBroker(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(tokenResponse{
			Token:     "ghs_short",
			ExpiresAt: time.Now().Add(10 * time.Second),
		})
	})

	for range 2 {
		if _, err := b.token(context.Background(), "10.64.0.7", gitReq()); err != nil {
			t.Fatal(err)
		}
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("broker called %d times, want 2 (the first token was inside the skew)", n)
	}
}

func TestBrokerKeysOnWrite(t *testing.T) {
	var calls atomic.Int64
	b := fakeBroker(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var tr tokenRequest
		_ = json.NewDecoder(r.Body).Decode(&tr)
		tok := "ghs_read"
		if tr.Write {
			tok = "ghs_write"
		}
		_ = json.NewEncoder(w).Encode(tokenResponse{Token: tok, ExpiresAt: time.Now().Add(time.Hour)})
	})

	read, err := b.token(context.Background(), "10.64.0.7", gitReq())
	if err != nil {
		t.Fatal(err)
	}
	write := gitReq()
	write.write = true
	got, err := b.token(context.Background(), "10.64.0.7", write)
	if err != nil {
		t.Fatal(err)
	}
	if read == got {
		t.Fatal("a read token was reused for a write")
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("broker called %d times, want 2", n)
	}
}

func TestBrokerDoesNotCacheDenials(t *testing.T) {
	var calls atomic.Int64
	b := fakeBroker(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(brokerError{Message: "not attached"})
			return
		}
		_ = json.NewEncoder(w).Encode(tokenResponse{Token: "ghs_ok", ExpiresAt: time.Now().Add(time.Hour)})
	})

	_, err := b.token(context.Background(), "10.64.0.7", gitReq())
	var d *denial
	if !errors.As(err, &d) || d.status != http.StatusForbidden {
		t.Fatalf("first call: %v", err)
	}

	tok, err := b.token(context.Background(), "10.64.0.7", gitReq())
	if err != nil {
		t.Fatalf("a denial was cached: %v", err)
	}
	if tok != "ghs_ok" {
		t.Fatalf("token = %q", tok)
	}
}

func TestBrokerSingleFlight(t *testing.T) {
	var calls atomic.Int64
	release := make(chan struct{})
	b := fakeBroker(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		<-release
		_ = json.NewEncoder(w).Encode(tokenResponse{Token: "ghs_one", ExpiresAt: time.Now().Add(time.Hour)})
	})

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = b.token(context.Background(), "10.64.0.7", gitReq())
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("broker called %d times, want 1", n)
	}
}
