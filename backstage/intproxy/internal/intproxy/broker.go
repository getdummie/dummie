package intproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type tokenRequest struct {
	VMIP        string `json:"vm_ip"`
	Integration string `json:"integration"`
	Repo        string `json:"repo,omitempty"`
	Write       bool   `json:"write"`
}

type tokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Account   string    `json:"account,omitempty"`
}

type brokerError struct {
	Message string `json:"message"`
}

// denial is a refusal we can render to the caller. It is separate from a
// transport error so the two get different status codes and different text.
type denial struct {
	status  int
	message string
}

func (d *denial) Error() string { return d.message }

type cacheKey struct {
	vmIP  string
	repo  string
	write bool
}

type cacheEntry struct {
	ready     chan struct{}
	token     string
	expiresAt time.Time
	err       error
}

type broker struct {
	hc      *http.Client
	skew    time.Duration
	console string

	mu    sync.Mutex
	cache map[cacheKey]*cacheEntry
}

func newBroker(cfg *Config) *broker {
	d := net.Dialer{Timeout: 5 * time.Second}
	return &broker{
		hc: &http.Client{
			Timeout: cfg.Broker.Timeout.Or(5 * time.Second),
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return d.DialContext(ctx, "unix", cfg.Broker.Socket)
				},
			},
		},
		skew:    cfg.Broker.TokenSkew.Or(60 * time.Second),
		console: cfg.ConsoleURL,
		cache:   map[cacheKey]*cacheEntry{},
	}
}

// token returns a cached token or mints one. Denials are never cached: a repo
// attached a second ago must work on the next request.
func (b *broker) token(ctx context.Context, vmIP string, req request) (string, error) {
	key := cacheKey{vmIP: vmIP, repo: req.repoSlug(), write: req.write}

	b.mu.Lock()
	if e, ok := b.cache[key]; ok {
		select {
		case <-e.ready:
			if e.err == nil && time.Until(e.expiresAt) > b.skew {
				b.mu.Unlock()
				return e.token, nil
			}
			delete(b.cache, key)
		default:
			b.mu.Unlock()
			select {
			case <-e.ready:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			if e.err != nil {
				return "", e.err
			}
			return e.token, nil
		}
	}
	entry := &cacheEntry{ready: make(chan struct{})}
	b.cache[key] = entry
	b.mu.Unlock()

	entry.token, entry.expiresAt, entry.err = b.fetch(ctx, vmIP, req)
	close(entry.ready)

	if entry.err != nil {
		b.mu.Lock()
		if b.cache[key] == entry {
			delete(b.cache, key)
		}
		b.mu.Unlock()
		return "", entry.err
	}
	return entry.token, nil
}

func (b *broker) fetch(ctx context.Context, vmIP string, req request) (string, time.Time, error) {
	body, err := json.Marshal(tokenRequest{
		VMIP:        vmIP,
		Integration: "github",
		Repo:        req.repoSlug(),
		Write:       req.write,
	})
	if err != nil {
		return "", time.Time{}, err
	}

	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://broker/v1/token", bytes.NewReader(body))
	if err != nil {
		return "", time.Time{}, err
	}
	hreq.Header.Set("Content-Type", "application/json")

	resp, err := b.hc.Do(hreq)
	if err != nil {
		return "", time.Time{}, &denial{
			status:  http.StatusBadGateway,
			message: "intproxy: could not reach the control server, so this request can be retried",
		}
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, b.denialFor(resp, raw)
	}

	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil || tr.Token == "" {
		return "", time.Time{}, &denial{
			status:  http.StatusBadGateway,
			message: "intproxy: the control server returned an unusable token",
		}
	}
	return tr.Token, tr.ExpiresAt, nil
}

func (b *broker) denialFor(resp *http.Response, raw []byte) error {
	var be brokerError
	_ = json.Unmarshal(raw, &be)
	msg := strings.TrimSpace(be.Message)

	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusConflict:
		if msg == "" {
			msg = "this vm is not attached to an integration that allows this repository"
		}
		return &denial{status: http.StatusForbidden, message: b.withConsole(msg)}
	case http.StatusTooManyRequests:
		return &denial{
			status:  http.StatusServiceUnavailable,
			message: "intproxy: github is rate limiting this integration, so this request can be retried shortly",
		}
	default:
		return &denial{
			status:  http.StatusBadGateway,
			message: "intproxy: the control server could not issue a credential for this request",
		}
	}
}

func (b *broker) withConsole(msg string) string {
	if b.console == "" {
		return "intproxy: " + msg
	}
	return fmt.Sprintf("intproxy: %s — manage integrations at %s/integrations", msg, b.console)
}
