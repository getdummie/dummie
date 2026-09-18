package credential

import (
	"context"
	"sync"
	"time"
)

type entry struct {
	ready chan struct{}
	tok   Token
	err   error
}

// Cache sits in front of any Source. The single-flight behaviour matters on a
// cold start: a clone opens several connections at once and they would
// otherwise each mint a token.
type Cache struct {
	src  Source
	skew time.Duration

	mu      sync.Mutex
	entries map[Scope]*entry
}

func NewCache(src Source, skew time.Duration) *Cache {
	if skew <= 0 {
		skew = time.Minute
	}
	return &Cache{src: src, skew: skew, entries: map[Scope]*entry{}}
}

// Token returns a cached token or mints one. Denials are never cached: a
// repository attached a second ago has to work on the very next request.
func (c *Cache) Token(ctx context.Context, s Scope) (Token, error) {
	c.mu.Lock()
	if e, ok := c.entries[s]; ok {
		select {
		case <-e.ready:
			if e.err == nil && !e.tok.stale(c.skew) {
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
