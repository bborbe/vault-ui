// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package launchregistry reproduces the Python vault_ui.launch_registry
// process-local registry of in-flight and finished launches.
//
// The server knows which launches are in flight; that knowledge — not the
// contents of a vault file the server does not exclusively own — decides
// whether a card shows "Starting…". A launch turn records Begin before writing
// the durable frontmatter marker and Finish when the turn returns (success or
// failure alike). A Finished record makes the list endpoints suppress a
// resurrected marker and drives the cleanup sweep to re-clear it from disk; a
// record is evicted once the sweep confirms the marker is gone from the file.
//
// The registry is in-memory and never persisted. It is sound only because the
// server runs a single worker (the Python backend calls uvicorn.run with no
// workers=): multiple workers would each hold their own registry and silently
// reintroduce the bug it exists to solve. The Go port preserves that
// single-process assumption and must not be shared across workers.
package launchregistry

import "sync"

// State is the recorded launch state.
type State string

const (
	// InFlight marks a launch whose turn has begun but not yet returned.
	InFlight State = "in_flight"
	// Finished marks a launch whose turn has returned (success or failure).
	Finished State = "finished"
)

// FinishedRecord is one finished launch: the item id and its kind.
type FinishedRecord struct {
	ItemID string
	Kind   string
}

// Registry is the process-local launch registry. It is safe for concurrent use
// and is sound only under the single-process server assumption (see the package
// doc comment).
type Registry interface {
	// Begin records that a launch turn for (vault, itemID) is in flight. It
	// overwrites any prior record for the key — a new launch supersedes an old
	// one — and clears any take-over mark left by the previous launch.
	Begin(vault, itemID, kind string)
	// MarkTakenOver records that a take-over from the wall ended the launch turn.
	MarkTakenOver(vault, itemID string)
	// WasTakenOver reports whether a take-over ended the launch whose request is
	// still pending.
	WasTakenOver(vault, itemID string) bool
	// Finish marks the launch finished (its turn has returned). It is a no-op
	// when no record exists.
	Finish(vault, itemID string)
	// State returns the recorded state, and false when no record exists.
	State(vault, itemID string) (State, bool)
	// Evict drops the record once the sweep confirms the marker is gone.
	Evict(vault, itemID string)
	// EvictIfFinished drops the record only when it is still Finished, returning
	// whether it removed one.
	EvictIfFinished(vault, itemID string) bool
	// Finished returns the finished records for a vault. The order is
	// unspecified.
	Finished(vault string) []FinishedRecord
	// Size is the number of records across all vaults.
	Size() int
}

// key identifies one launch: the vault and the item id. The key carries no item
// kind, so a task and a goal sharing the same id in one vault share a record.
type key struct {
	vault  string
	itemID string
}

// record is the state and kind stored for one key.
type record struct {
	state State
	kind  string
}

type registry struct {
	mu        sync.Mutex
	records   map[key]record
	takenOver map[key]struct{}
}

// NewRegistry creates an empty launch registry.
func NewRegistry() Registry {
	return &registry{
		records:   map[key]record{},
		takenOver: map[key]struct{}{},
	}
}

func (r *registry) Begin(vault, itemID, kind string) {
	k := key{vault: vault, itemID: itemID}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records[k] = record{state: InFlight, kind: kind}
	delete(r.takenOver, k)
}

func (r *registry) MarkTakenOver(vault, itemID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.takenOver[key{vault: vault, itemID: itemID}] = struct{}{}
}

func (r *registry) WasTakenOver(vault, itemID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.takenOver[key{vault: vault, itemID: itemID}]
	return ok
}

func (r *registry) Finish(vault, itemID string) {
	k := key{vault: vault, itemID: itemID}
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.records[k]
	if !ok {
		return
	}
	r.records[k] = record{state: Finished, kind: existing.kind}
}

func (r *registry) State(vault, itemID string) (State, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.records[key{vault: vault, itemID: itemID}]
	if !ok {
		return "", false
	}
	return existing.state, true
}

func (r *registry) Evict(vault, itemID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.records, key{vault: vault, itemID: itemID})
}

// EvictIfFinished re-checks the state and deletes in one synchronous step under
// the registry lock. The cleanup sweep decides what to evict from a snapshot
// taken before it awaits a clear subprocess; during that await a concurrent
// launch can call Begin for the same id, flipping the record back to InFlight,
// and an unconditional evict would delete that fresh record. There is no await
// between the check and the delete, so under the single-worker assumption no
// caller can interleave.
func (r *registry) EvictIfFinished(vault, itemID string) bool {
	k := key{vault: vault, itemID: itemID}
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.records[k]
	if !ok || existing.state != Finished {
		return false
	}
	delete(r.records, k)
	return true
}

func (r *registry) Finished(vault string) []FinishedRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	finished := []FinishedRecord{}
	for k, rec := range r.records {
		if k.vault == vault && rec.state == Finished {
			finished = append(finished, FinishedRecord{ItemID: k.itemID, Kind: rec.kind})
		}
	}
	return finished
}

func (r *registry) Size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.records)
}
