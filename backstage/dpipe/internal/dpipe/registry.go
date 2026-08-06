package dpipe

import (
	"context"
	"sync"
)

// Registry counts active connections — copies, SSH relays, TLS sessions and
// forwarded connections all count identically — and tracks the draining flag so
// an upgraded-away process knows when it can exit.
type Registry struct {
	mu       sync.Mutex
	active   map[string]struct{}
	draining bool
	waiters  []chan struct{}

	forwardCount func() int
}

// NewRegistry returns an empty registry. forwardCount, if non-nil, supplies the
// number of active listen_forwards for status replies.
func NewRegistry(forwardCount func() int) *Registry {
	return &Registry{active: map[string]struct{}{}, forwardCount: forwardCount}
}

// Add registers an active connection.
func (r *Registry) Add(id string) {
	r.mu.Lock()
	r.active[id] = struct{}{}
	r.mu.Unlock()
}

// Done unregisters an active connection and wakes drain waiters at zero.
func (r *Registry) Done(id string) {
	r.mu.Lock()
	delete(r.active, id)
	if len(r.active) == 0 {
		for _, ch := range r.waiters {
			close(ch)
		}
		r.waiters = nil
	}
	r.mu.Unlock()
}

// Active returns the number of active connections.
func (r *Registry) Active() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.active)
}

// SetDraining marks the process as draining.
func (r *Registry) SetDraining() {
	r.mu.Lock()
	r.draining = true
	r.mu.Unlock()
}

// Draining reports the draining flag.
func (r *Registry) Draining() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.draining
}

// Snapshot returns the fields of a status reply.
func (r *Registry) Snapshot() (active, forwards int, draining bool) {
	r.mu.Lock()
	active, draining = len(r.active), r.draining
	r.mu.Unlock()
	if r.forwardCount != nil {
		forwards = r.forwardCount()
	}
	return
}

// WaitZero blocks until there are no active connections or ctx is done.
func (r *Registry) WaitZero(ctx context.Context) error {
	r.mu.Lock()
	if len(r.active) == 0 {
		r.mu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	r.waiters = append(r.waiters, ch)
	r.mu.Unlock()

	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
