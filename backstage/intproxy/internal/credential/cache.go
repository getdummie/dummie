package credential

import (
	"context"
	"sync"
	"time"
)

type entry struct {
	ready chan struct{}
	at    time.Time
	tok   Token
	err   error
}

// Cache sits in front of any Source. The single-flight behaviour matters on a
// cold start: a clone opens several connections at once and they would
// otherwise each mint a token.
type Cache struct {
	src    Source
	skew   time.Duration
	maxAge time.Duration

	mu      sync.Mutex
	entries map[Scope]*entry
}

func NewCache(src Source, skew, maxAge time.Duration) *Cache {
	if skew <= 0 {
		skew = time.Minute
	}
	if maxAge <= 0 {
		maxAge = time.Minute
	}
	return &Cache{src: src, skew: skew, maxAge: maxAge, entries: map[Scope]*entry{}}
}

// reusable is deliberately two rules. Expiry keeps the token valid; maxAge
// bounds how long a grant that has since been revoked keeps working, because
// the answer lives in the source and nothing pushes a change back here. Without
// it a revocation would not bite until the token expired, which for a github
// installation token is an hour.
func (c *Cache) reusable(e *entry) bool {
	if e.err != nil {
		return false
	}
	if time.Since(e.at) >= c.maxAge {
		return false
	}
	return !e.tok.stale(c.skew)
}

// Token returns a cached token or mints one. Denials are never cached: a
// repository attached a second ago has to work on the very next request.
func (c *Cache) Token(ctx context.Context, s Scope) (Token, error) {
	c.mu.Lock()
	if e, ok := c.entries[s]; ok {
		select {
		case <-e.ready:
			if c.reusable(e) {
				c.mu.Unlock()
				return e.tok, nil
			}
			delete(c.entries, s)
		default:
			c.mu.Unlock()
			select {
			case <-e.ready:
			case <-ctx.Done():
				return Token{}, ctx.Err()
			}
			return e.tok, e.err
		}
	}
	e := &entry{ready: make(chan struct{})}
	c.entries[s] = e
	c.mu.Unlock()

	e.tok, e.err = c.src.Token(ctx, s)
	e.at = time.Now()
	close(e.ready)

	if e.err != nil {
		c.mu.Lock()
		if c.entries[s] == e {
			delete(c.entries, s)
		}
		c.mu.Unlock()
		return Token{}, e.err
	}
	return e.tok, nil
}
