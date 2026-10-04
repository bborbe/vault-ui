// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package sessionlock reproduces the Python vault_ui.session_lock_registry
// process-local registry of per-(vault, item) locks.
//
// set_task_session's read-check-write — show_task, the UUID-overwrite guard,
// set_field — must be one critical section, or two concurrent PATCHes can both
// read an empty value, both pass the guard, and the later write wins: a lost
// update. This registry hands out one lock per (vault, item) so unrelated items
// never serialise against each other.
//
// The registry is in-memory and never persisted. It is sound only because the
// server runs a single worker (the Python backend calls uvicorn.run with no
// workers=): multiple workers would each hold their own registry and silently
// reintroduce the race the lock exists to close. The Go port preserves that
// single-process assumption and must not be shared across workers.
//
// The lock is a best-effort guard, not an atomic one: vault-ui is only one of
// several processes that write these frontmatter files, so an in-process lock
// cannot serialise vault-ui against those writers; it only stops vault-ui from
// racing itself.
package sessionlock

import (
	"context"
	"sync"
)

// Registry hands out one lock per (vault, itemID).
type Registry interface {
	// Lock acquires the lock for (vault, itemID) and returns a release func the
	// caller MUST invoke (typically deferred) to leave the critical section. A
	// cancelled ctx unwinds the waiter's holder count and returns ctx.Err() with
	// a nil release.
	Lock(ctx context.Context, vault, itemID string) (release func(), err error)
	// Size is the number of tracked entries across all keys.
	Size() int
}

// key identifies one lock: the vault and the item id.
type key struct {
	vault  string
	itemID string
}

// entry is a per-key semaphore plus a count of its active users.
//
// holders counts every caller inside the critical section or queued waiting. A
// channel exposes no waiter count, so without this counter the registry cannot
// tell "somebody still holds or awaits this lock" from "nobody does", and could
// evict an entry while a caller still depends on it.
type entry struct {
	sem     chan struct{}
	holders int
}

type registry struct {
	mu      sync.Mutex
	entries map[key]*entry
}

// NewRegistry creates an empty session-lock registry.
func NewRegistry() Registry {
	return &registry{entries: map[key]*entry{}}
}

// Lock acquires the per-key semaphore, counting the caller as a holder from the
// moment it is tracked — before it waits — so an entry a caller still awaits is
// never evicted. The buffered channel supports the cancellation a bare
// sync.Mutex cannot: a waiter removed by ctx cancellation unwinds its count and
// returns ctx.Err() with a nil release.
func (r *registry) Lock(ctx context.Context, vault, itemID string) (func(), error) {
	k := key{vault: vault, itemID: itemID}

	r.mu.Lock()
	e, ok := r.entries[k]
	if !ok {
		e = &entry{sem: make(chan struct{}, 1)}
		r.entries[k] = e
	}
	e.holders++
	r.mu.Unlock()

	select {
	case e.sem <- struct{}{}:
	case <-ctx.Done():
		r.unwind(k, e)
		return nil, ctx.Err()
	}

	return func() {
		<-e.sem
		r.unwind(k, e)
	}, nil
}

// unwind decrements the entry's holder count and evicts it once the count
// returns to zero (the last holder or waiter has left). The decrement-and-delete
// is one synchronous step under the registry lock, so an entry a caller still
// holds or awaits is never deleted and replaced by a different lock object.
func (r *registry) unwind(k key, e *entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e.holders--
	if e.holders == 0 {
		delete(r.entries, k)
	}
}

func (r *registry) Size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}
