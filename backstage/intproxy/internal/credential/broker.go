package credential

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type BrokerConfig struct {
	Socket  string
	Timeout time.Duration
}

// Broker asks a unix socket for a token. It holds no credential of its own:
// whatever serves that socket adds one and decides whether to answer, so
// reaching the socket is the whole of this proxy's authority.
type Broker struct {
	hc   *http.Client
	docs string
}

func NewBroker(cfg BrokerConfig, docsURL string) *Broker {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	return &Broker{
		hc: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return d.DialContext(ctx, "unix", cfg.Socket)
				},
			},
		},
		docs: docsURL,
	}
}

type brokerRequest struct {
	VMIP        string `json:"vm_ip"`
	Integration string `json:"integration"`
	Repo        string `json:"repo,omitempty"`
	Write       bool   `json:"write"`
}

type brokerResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Account   string    `json:"account,omitempty"`
}

type brokerError struct {
	Message string `json:"message"`
}

func (b *Broker) Token(ctx context.Context, s Scope) (Token, error) {
	body, err := json.Marshal(brokerRequest{
		VMIP:        s.ClientIP,
		Integration: s.Integration,
		Repo:        s.Resource,
		Write:       s.Write,
	})
	if err != nil {
		return Token{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://broker/v1/token", bytes.NewReader(body))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.hc.Do(req)
	if err != nil {
		return Token{}, &Denial{
			Status:  http.StatusBadGateway,
			Message: "intproxy: could not reach the control server, so this request can be retried",
		}
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return Token{}, b.denialFor(resp.StatusCode, raw)
	}

	var br brokerResponse
	if err := json.Unmarshal(raw, &br); err != nil || br.Token == "" {
		return Token{}, &Denial{
			Status:  http.StatusBadGateway,
			Message: "intproxy: the control server returned an unusable token",
		}
	}
	return Token{Value: br.Token, ExpiresAt: br.ExpiresAt}, nil
}

func (b *Broker) denialFor(status int, raw []byte) error {
	var be brokerError
	_ = json.Unmarshal(raw, &be)
	msg := strings.TrimSpace(be.Message)

	switch status {
	case http.StatusForbidden, http.StatusConflict:
		if msg == "" {
			msg = "this vm is not attached to an integration that allows this repository"
		}
		return &Denial{Status: http.StatusForbidden, Message: b.withDocs(msg)}
	case http.StatusTooManyRequests:
		return &Denial{
			Status:  http.StatusServiceUnavailable,
			Message: "intproxy: the upstream is rate limiting this integration, so this request can be retried shortly",
		}
	default:
		return &Denial{
			Status:  http.StatusBadGateway,
			Message: "intproxy: the control server could not issue a credential for this request",
		}
	}
}

func (b *Broker) withDocs(msg string) string {
	if b.docs == "" {
		return "intproxy: " + msg
	}
	return "intproxy: " + msg + " — manage integrations at " + b.docs
}
