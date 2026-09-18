// Package credential turns a request's scope into a usable token. It knows
// nothing about any particular integration: the scope is opaque strings.
package credential

import (
	"context"
	"fmt"
	"time"
)

// Scope is everything that decides which token a request gets. It is also the
// cache key, so any field that changes the answer has to live here.
type Scope struct {
	// Integration is the name the request was routed to, "github" and so on.
	Integration string
	// ClientIP is the source address of the connection, which is the only
	// identity this proxy has.
	ClientIP string
	// Credential names which configured credential to use. Empty under broker
	// mode, where whatever serves the socket decides.
	Credential string
	// Resource is integration-defined: a repository slug for github. Empty
	// means the request named none, which cannot be authorized per resource.
	Resource string
	Write    bool
}

type Token struct {
	Value string
	// ExpiresAt zero means a static credential that never rotates.
	ExpiresAt time.Time
}

func (t Token) stale(skew time.Duration) bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return time.Until(t.ExpiresAt) <= skew
}

type Source interface {
	Token(context.Context, Scope) (Token, error)
}

// Denial is a refusal that can be rendered to the caller, as opposed to a
// transport failure: the two get different statuses and different text.
type Denial struct {
	Status  int
	Message string
}

func (d *Denial) Error() string { return d.Message }

func Denyf(status int, format string, a ...any) *Denial {
	return &Denial{Status: status, Message: fmt.Sprintf(format, a...)}
}
