package credential

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingSource struct {
	calls atomic.Int64
	fn    func(Scope) (Token, error)
}

func (c *countingSource) Token(_ context.Context, s Scope) (Token, error) {
	c.calls.Add(1)
	return c.fn(s)
}

func gitScope() Scope {
	return Scope{Integration: "github", ClientIP: "10.64.0.7", Resource: "getdummie/dummie"}
}

func TestCacheHoldsUntilSkew(t *testing.T) {
	src := &countingSource{fn: func(Scope) (Token, error) {
		return Token{Value: "ghs_one", ExpiresAt: time.Now().Add(time.Hour)}, nil
	}}
	c := NewCache(src, time.Minute, time.Minute)

	for range 3 {
		tok, err := c.Token(context.Background(), gitScope())
		if err != nil {
			t.Fatal(err)
		}
		if tok.Value != "ghs_one" {
			t.Fatalf("token = %q", tok.Value)
		}
	}
	if n := src.calls.Load(); n != 1 {
		t.Fatalf("source called %d times, want 1", n)
	}
}

func TestCacheRefetchesInsideSkew(t *testing.T) {
	src := &countingSource{fn: func(Scope) (Token, error) {
		return Token{Value: "ghs_short", ExpiresAt: time.Now().Add(10 * time.Second)}, nil
	}}
	c := NewCache(src, time.Minute, time.Minute)

	for range 2 {
		if _, err := c.Token(context.Background(), gitScope()); err != nil {
			t.Fatal(err)
		}
	}
	if n := src.calls.Load(); n != 2 {
		t.Fatalf("source called %d times, want 2 (the first token was inside the skew)", n)
	}
}

// A static token never rotates, so a zero expiry must not read as expired.
func TestCacheHoldsATokenThatNeverExpires(t *testing.T) {
	src := &countingSource{fn: func(Scope) (Token, error) {
		return Token{Value: "ghp_static"}, nil
	}}
	c := NewCache(src, time.Minute, time.Minute)

	for range 3 {
		if _, err := c.Token(context.Background(), gitScope()); err != nil {
			t.Fatal(err)
		}
	}
	if n := src.calls.Load(); n != 1 {
		t.Fatalf("source called %d times, want 1", n)
	}
}

// Nothing pushes a revocation here, so a grant has to be re-asked even while
// its token is still perfectly valid. Without this a repository taken away
// would keep working until the token expired -- an hour, for github.
func TestCacheReasksAfterMaxAgeWhileTheTokenIsStillValid(t *testing.T) {
	var granted atomic.Bool
	granted.Store(true)

	src := &countingSource{fn: func(Scope) (Token, error) {
		if !granted.Load() {
			return Token{}, &Denial{Status: http.StatusForbidden, Message: "revoked"}
		}
		// Far from expiry, so only max_age can force a re-ask.
		return Token{Value: "ghs_one", ExpiresAt: time.Now().Add(time.Hour)}, nil
	}}
	c := NewCache(src, time.Minute, 20*time.Millisecond)

	if _, err := c.Token(context.Background(), gitScope()); err != nil {
		t.Fatal(err)
	}
	granted.Store(false)

	// Still inside max_age: the cached grant is reused.
	if _, err := c.Token(context.Background(), gitScope()); err != nil {
		t.Fatalf("the cached token was dropped too early: %v", err)
	}

	time.Sleep(30 * time.Millisecond)

	_, err := c.Token(context.Background(), gitScope())
	var d *Denial
	if !errors.As(err, &d) {
		t.Fatalf("a revoked grant was still served after max_age: %v", err)
	}
}

func TestCacheKeysOnEveryScopeField(t *testing.T) {
	src := &countingSource{fn: func(s Scope) (Token, error) {
		return Token{Value: s.Integration + "/" + s.Credential + "/" + s.Resource, ExpiresAt: time.Now().Add(time.Hour)}, nil
	}}
	c := NewCache(src, time.Minute, time.Minute)

	base := gitScope()
	scopes := []Scope{base, {}, {}, {}, {}}
	scopes[1] = base
	scopes[1].Write = true
	scopes[2] = base
	scopes[2].Credential = "other"
	scopes[3] = base
	scopes[3].Integration = "llm"
	scopes[4] = base
	scopes[4].ClientIP = "10.64.0.8"

	for _, s := range scopes {
		if _, err := c.Token(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	if n := src.calls.Load(); n != int64(len(scopes)) {
		t.Fatalf("source called %d times, want %d: two different scopes shared an entry", n, len(scopes))
	}
}

func TestCacheDoesNotCacheDenials(t *testing.T) {
	src := &countingSource{}
	src.fn = func(Scope) (Token, error) {
		if src.calls.Load() == 1 {
			return Token{}, &Denial{Status: http.StatusForbidden, Message: "not attached"}
		}
		return Token{Value: "ghs_ok", ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	c := NewCache(src, time.Minute, time.Minute)

	_, err := c.Token(context.Background(), gitScope())
	var d *Denial
	if !errors.As(err, &d) || d.Status != http.StatusForbidden {
		t.Fatalf("first call: %v", err)
	}

	tok, err := c.Token(context.Background(), gitScope())
	if err != nil {
		t.Fatalf("a denial was cached: %v", err)
	}
	if tok.Value != "ghs_ok" {
		t.Fatalf("token = %q", tok.Value)
	}
}

func TestCacheSingleFlight(t *testing.T) {
	release := make(chan struct{})
	src := &countingSource{fn: func(Scope) (Token, error) {
		<-release
		return Token{Value: "ghs_one", ExpiresAt: time.Now().Add(time.Hour)}, nil
	}}
	c := NewCache(src, time.Minute, time.Minute)

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = c.Token(context.Background(), gitScope())
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
	if n := src.calls.Load(); n != 1 {
		t.Fatalf("source called %d times, want 1", n)
	}
}
