package dpipe

import (
	"strings"
	"sync"
	"time"
)

// gnome-remote-desktop's system mode hands a client from the login screen to the
// user session with a Server Redirection PDU, and the client reconnects replaying
// the PDU's load balance info as an X.224 routing token. That reconnect carries
// one-time credentials the proxy cannot verify, so it is authorised by the token
// instead: possession proves the client held a session dproxy already approved.
//
// Entries are single-use and short-lived, because the token travels in the clear
// on the reconnect.
const defaultHandoverTTL = 30 * time.Second

type handoverEntry struct {
	target  string
	expires time.Time
}

type handoverRegistry struct {
	mu  sync.Mutex
	m   map[string]handoverEntry
	ttl time.Duration
	now func() time.Time
}

func newHandoverRegistry(ttl time.Duration) *handoverRegistry {
	if ttl <= 0 {
		ttl = defaultHandoverTTL
	}
	return &handoverRegistry{m: map[string]handoverEntry{}, ttl: ttl, now: time.Now}
}

func (r *handoverRegistry) put(token []byte, target string) {
	key := normalizeRoutingToken(string(token))
	if key == "" || target == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	r.m[key] = handoverEntry{target: target, expires: r.now().Add(r.ttl)}
}

// take consumes a token. The second return distinguishes "not a handover" from
// "a handover that expired", which are worth logging differently.
func (r *handoverRegistry) take(cookie string) (string, bool) {
	key := normalizeRoutingToken(cookie)
	if key == "" {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.m[key]
	if !ok {
		return "", false
	}
	delete(r.m, key)
	if r.now().After(e.expires) {
		return "", false
	}
	return e.target, true
}

func (r *handoverRegistry) pruneLocked() {
	now := r.now()
	for k, e := range r.m {
		if now.After(e.expires) {
			delete(r.m, k)
		}
	}
}

// normalizeRoutingToken strips the framing the two sides disagree about: the
// redirection PDU carries the token with its cookie prefix and CRLF, while the
// parsed connection request has already had the CRLF removed.
func normalizeRoutingToken(s string) string {
	s = strings.TrimSuffix(s, "\r\n")
	s = strings.TrimPrefix(s, "Cookie: msts=")
	return strings.TrimSpace(s)
}
