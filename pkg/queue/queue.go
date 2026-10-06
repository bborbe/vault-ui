// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package queue holds vault-ui's pending frontmatter writes in memory, one FIFO
// per vault.
//
// Each vault's writes are applied one at a time, in submission order, by that
// vault's own consumer goroutine; a vault with nothing pending runs no
// background work at all, and a write to one vault is never held up by another
// vault's backlog. Enqueue never waits for the write to be applied, so the
// request that asked for it can answer at once.
//
// Nothing is persisted: a crash loses every write that had not been applied
// yet, and the vault keeps its last written state. On shutdown the consumers
// stop applying and drop what is left, logging the count per vault.
//
// The seam is deliberately three operations wide — Enqueue, Consume, Done — and
// is to be replaced by libqueue when that library lands. Shutdown has no
// timeout: Consume blocks until every in-flight Apply has returned, even one
// that ignores its cancelled context, and the service manager escalates to
// SIGKILL if a write never comes back.
package queue

import (
	"context"
	stderrors "errors"
	"sync"

	"github.com/bborbe/errors"
	"github.com/bborbe/run"
	"github.com/golang/glog"
)

// Write is one queued vault write.
type Write struct {
	// Vault is the configured vault name. It keys the FIFO the write joins;
	// writes to different vaults never wait on each other.
	Vault string
	// ItemID names the task or goal the write targets. It is used for logs only.
	ItemID string
	// Apply performs the write and its post-write side-effects. The vault's
	// consumer calls it with the consumer context passed to Consume — never
	// with the context passed to Enqueue.
	Apply run.Func
}

// ErrClosed is returned (wrapped) by Enqueue once Consume has returned.
var ErrClosed = stderrors.New("write queue closed")

//counterfeiter:generate -o ./mocks/queue-queue.go --fake-name Queue . Queue

// Queue is vault-ui's in-tree producer/consumer seam for vault writes.
type Queue interface {
	// Enqueue appends write to its vault's FIFO and returns without waiting
	// for it to be applied. The queue is unbounded: an accepted write is never
	// dropped while Consume runs.
	Enqueue(ctx context.Context, write Write) error
	// Consume applies queued writes until ctx is cancelled: one consumer
	// goroutine per vault with pending writes, each applying its vault's writes
	// one at a time in submission order. It returns nil after ctx is cancelled
	// and every in-flight Apply has returned.
	Consume(ctx context.Context) error
	// Done returns a channel that is closed once the vault has no pending and
	// no in-flight write. For an idle or unknown vault it returns an
	// already-closed channel.
	Done(vault string) <-chan struct{}
}

// closedChan is the already-closed channel Done hands out for a vault that has
// no pending and no in-flight write. It is created once and never closed again.
var closedChan = func() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}()

// vaultQueue is one vault's pending writes and consumer state.
type vaultQueue struct {
	// pending is the unbounded FIFO of writes not yet applied.
	pending []Write
	// running reports that a consumer goroutine is active for this vault.
	running bool
	// done is created when the vault goes from idle to busy and closed when it
	// becomes idle again.
	done chan struct{}
}

type queue struct {
	mu     sync.Mutex
	vaults map[string]*vaultQueue
	// consumerCtx is the context Consume was called with; nil until it starts.
	consumerCtx context.Context
	closed      bool
	wg          sync.WaitGroup
}

// NewQueue returns an empty Queue. Writes enqueued before Consume starts are
// held and applied once it does.
func NewQueue() Queue {
	return &queue{
		vaults: map[string]*vaultQueue{},
	}
}

// Enqueue appends write to its vault's FIFO and returns without waiting for it
// to be applied. It rejects a write with an empty vault or a nil Apply, and
// once Consume has returned it rejects every write with a wrapped ErrClosed.
func (q *queue) Enqueue(ctx context.Context, write Write) error {
	if write.Vault == "" {
		return errors.Errorf(ctx, "enqueue write: empty vault")
	}
	if write.Apply == nil {
		return errors.Errorf(ctx, "enqueue write for vault %s: nil Apply", write.Vault)
	}

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return errors.Wrapf(ctx, ErrClosed, "enqueue write for vault %s", write.Vault)
	}
	vault := q.vaultLocked(write.Vault)
	if len(vault.pending) == 0 && !vault.running {
		vault.done = make(chan struct{})
	}
	vault.pending = append(vault.pending, write)
	depth := len(vault.pending)
	consumerCtx := q.consumerCtx
	if consumerCtx != nil && !vault.running {
		vault.running = true
		q.wg.Add(1)
		// Deliberate exception to go-concurrency/no-raw-go-func: consumers are
		// spawned per vault on demand, and none of the run strategies express a
		// spawn that is unbounded and never blocks the caller.
		go q.drain(consumerCtx, write.Vault)
	}
	q.mu.Unlock()

	glog.V(2).Infof("write queue vault=%s item=%s depth=%d", write.Vault, write.ItemID, depth)
	return nil
}

// Consume applies queued writes until ctx is cancelled. It returns nil after
// ctx is cancelled and every in-flight Apply has returned.
func (q *queue) Consume(ctx context.Context) error {
	q.mu.Lock()
	if q.consumerCtx != nil {
		q.mu.Unlock()
		return errors.New(ctx, "write queue already consuming")
	}
	q.consumerCtx = ctx
	for name, vault := range q.vaults {
		if len(vault.pending) > 0 && !vault.running {
			vault.running = true
			q.wg.Add(1)
			go q.drain(ctx, name)
		}
	}
	q.mu.Unlock()

	<-ctx.Done()

	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.wg.Wait()
	return nil
}

// Done returns a channel that is closed once the vault has no pending and no
// in-flight write. For an idle or unknown vault it returns an already-closed
// channel.
func (q *queue) Done(vault string) <-chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()
	entry, ok := q.vaults[vault]
	if !ok || entry.done == nil || (len(entry.pending) == 0 && !entry.running) {
		return closedChan
	}
	return entry.done
}

// drain is one vault's consumer. It applies that vault's writes one at a time
// in submission order until the consumer context is cancelled or the FIFO runs
// empty, then releases the vault so a later write can start a fresh consumer.
func (q *queue) drain(ctx context.Context, vault string) {
	defer q.wg.Done()
	for {
		q.mu.Lock()
		entry := q.vaults[vault]
		if entry == nil {
			q.mu.Unlock()
			return
		}
		if ctx.Err() != nil {
			dropped := len(entry.pending)
			entry.pending = nil
			entry.running = false
			close(entry.done)
			q.mu.Unlock()
			if dropped > 0 {
				glog.Warningf(
					"write queue vault=%s dropped %d unapplied writes on shutdown",
					vault,
					dropped,
				)
			}
			return
		}
		if len(entry.pending) == 0 {
			entry.running = false
			close(entry.done)
			q.mu.Unlock()
			return
		}
		write := entry.pending[0]
		entry.pending[0] = Write{}
		entry.pending = entry.pending[1:]
		q.mu.Unlock()

		q.applyOne(ctx, vault, write)
	}
}

// applyOne applies one write. A panic or an error is logged and swallowed: the
// consumer must survive both so the writes behind it still run.
func (q *queue) applyOne(ctx context.Context, vault string, write Write) {
	defer func() {
		if recovered := recover(); recovered != nil {
			glog.Errorf("write queue vault=%s item=%s panicked: %v", vault, write.ItemID, recovered)
		}
	}()
	if err := write.Apply(ctx); err != nil {
		glog.Warningf("write queue vault=%s item=%s failed: %v", vault, write.ItemID, err)
	}
}

// vaultLocked returns the vault's entry, creating it when missing. The caller
// must hold the mutex.
func (q *queue) vaultLocked(vault string) *vaultQueue {
	entry, ok := q.vaults[vault]
	if !ok {
		entry = &vaultQueue{}
		q.vaults[vault] = entry
	}
	return entry
}
