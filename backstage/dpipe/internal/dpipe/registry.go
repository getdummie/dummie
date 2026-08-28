package dpipe

import (
	"context"
	"sync"
)

type Registry struct {
	mu       sync.Mutex
	active   map[string]struct{}
	draining bool
	waiters  []chan struct{}

	forwardCount func() int
}

func NewRegistry(forwardCount func() int) *Registry {
	return &Registry{active: map[string]struct{}{}, forwardCount: forwardCount}
}

func (r *Registry) Add(id string) {
	r.mu.Lock()
	r.active[id] = struct{}{}
	r.mu.Unlock()
}

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

func (r *Registry) Active() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.active)
}

func (r *Registry) SetDraining() {
	r.mu.Lock()
	r.draining = true
	r.mu.Unlock()
}

func (r *Registry) Draining() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.draining
}

func (r *Registry) Snapshot() (active, forwards int, draining bool) {
	r.mu.Lock()
	active, draining = len(r.active), r.draining
	r.mu.Unlock()
	if r.forwardCount != nil {
		forwards = r.forwardCount()
	}
	return
}

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
