package dpipe

import (
	"testing"
	"time"
)

func TestHandoverTokenIsSingleUse(t *testing.T) {
	r := newHandoverRegistry(time.Minute)
	r.put([]byte("Cookie: msts=abc123\r\n"), "10.64.0.5:3389")

	target, ok := r.take("Cookie: msts=abc123")
	if !ok || target != "10.64.0.5:3389" {
		t.Fatalf("take = %q, %v; want the recorded target", target, ok)
	}
	if _, ok := r.take("Cookie: msts=abc123"); ok {
		t.Fatal("token was accepted twice")
	}
}

func TestHandoverTokenExpires(t *testing.T) {
	now := time.Now()
	r := newHandoverRegistry(30 * time.Second)
	r.now = func() time.Time { return now }
	r.put([]byte("abc123"), "10.64.0.5:3389")

	now = now.Add(31 * time.Second)
	if _, ok := r.take("abc123"); ok {
		t.Fatal("expired token was accepted")
	}
}

func TestHandoverIgnoresUnknownAndEmptyCookies(t *testing.T) {
	r := newHandoverRegistry(time.Minute)
	r.put([]byte("abc123"), "10.64.0.5:3389")

	for _, cookie := range []string{"", "Cookie: mstshash=someone", "abc124"} {
		if _, ok := r.take(cookie); ok {
			t.Fatalf("cookie %q was treated as a handover", cookie)
		}
	}
}
