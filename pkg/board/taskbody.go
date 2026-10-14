// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/bborbe/errors"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/api"
)

// TaskListBody is a rendered GET /api/tasks response in the two forms the
// handler can serve: Identity is the JSON body the board produces today,
// Gzipped is its gzip-compressed form. Both are immutable once returned and are
// shared with every reader.
type TaskListBody struct {
	Identity []byte
	Gzipped  []byte
}

// VaultSignal is one selected vault's published-rows signal. A held body is
// gated on the signals of exactly the vaults its query selects.
type VaultSignal struct {
	Name   string
	Signal uint64
}

// taskBodyParams carries the body cache's injectable dependencies.
type taskBodyParams struct {
	// Project renders the query's rows. Production: the board's ListTasks.
	Project func(ctx context.Context, query TaskQuery) ([]api.TaskResponse, error)
	// Encode marshals the projected rows into the identity body. Production:
	// encodeTaskList, which reproduces the handler's writeJSON exactly.
	Encode func(value any) ([]byte, error)
	// Signals returns, in selection order, the published-rows signal of every
	// vault the query selects. It changes when any selected vault's rows change or
	// when the selection changes, and is unaffected by a vault the query does not
	// select. Production: the board's taskSnapshotSignals.
	Signals func(selected []Vault) []VaultSignal
	// Clock reads the request time the clock boundary is derived from.
	// Production: the board's clock.
	Clock libtime.CurrentDateTimeGetter
	// Rows reads the snapshot's pre-filter rows for the query — the rows the
	// visibility filter has not yet dropped. Production: the board reads
	// b.snapshot.List per selected vault and concatenates them.
	Rows func(ctx context.Context, query TaskQuery) ([]taskSnapshotRow, error)
}

// taskBodyEntry is one held body. It is replaced wholesale on invalidation
// rather than mutated in place.
type taskBodyEntry struct {
	body TaskListBody
	// signals are the selected vaults' published-rows signals the body was built
	// from. The entry is served only while the query's current signals equal
	// these.
	signals []VaultSignal
	// lastUsed is the cache's monotonic access ordinal, which orders the held
	// bodies for least-recently-used eviction.
	lastUsed uint64
	// expiresAt is the first instant, strictly after the entry was built, at
	// which a clock-derived field of the body can change. The zero time means
	// the body has no clock-derived rows and never expires while its selected
	// vaults' signals hold.
	expiresAt time.Time
}

// taskBodyCacheMaxEntries bounds the number of held bodies. It is a fixed
// constant, never a configuration key: the per-query scoping removes the
// incidental bound the old blanket generation drop provided, so an
// unauthenticated client requesting many distinct queries must not be able to
// grow the held set without bound. Its value is local to this cache and
// reversible; it must be at least 2.
const taskBodyCacheMaxEntries = 64

// taskBodyCache holds one rendered body per (selected-vault signal vector,
// query). The held set is bounded by taskBodyCacheMaxEntries with
// least-recently-used eviction.
type taskBodyCache struct {
	mu      sync.Mutex
	entries map[string]*taskBodyEntry
	project func(ctx context.Context, query TaskQuery) ([]api.TaskResponse, error)
	encode  func(value any) ([]byte, error)
	// signals is the seam Get reads the selected vaults' signals from.
	signals func(selected []Vault) []VaultSignal
	clock   libtime.CurrentDateTimeGetter
	rows    func(ctx context.Context, query TaskQuery) ([]taskSnapshotRow, error)
	// lastUsedSeq is the cache's monotonic access ordinal. It is advanced under
	// the mutex on every touch and every store.
	lastUsedSeq uint64
}

// newTaskBodyCache returns an empty cache over the given seams.
func newTaskBodyCache(params taskBodyParams) *taskBodyCache {
	return &taskBodyCache{
		entries: map[string]*taskBodyEntry{},
		project: params.Project,
		encode:  params.Encode,
		signals: params.Signals,
		clock:   params.Clock,
		rows:    params.Rows,
	}
}

// Get returns the rendered body for the query, building and holding it on a
// miss. A hit performs no projection, no marshal and no compression. A held
// body is discarded when the query's selected-vault signals differ from the ones
// the body was built from, or when the clock boundary has passed; a build that
// started before a signal move is not held.
func (c *taskBodyCache) Get(
	ctx context.Context,
	query TaskQuery,
	selected []Vault,
) (TaskListBody, error) {
	key := taskBodyKey(query)
	now := c.clock.Now().UTC().Time()

	signals := c.signals(selected)

	c.mu.Lock()
	if entry, ok := c.entries[key]; ok {
		if slices.Equal(entry.signals, signals) &&
			(entry.expiresAt.IsZero() || now.Before(entry.expiresAt)) {
			body := entry.body
			c.touchLocked(entry)
			c.mu.Unlock()
			return body, nil
		}
		delete(c.entries, key)
	}
	c.mu.Unlock()

	responses, err := c.project(ctx, query)
	if err != nil {
		glog.Errorf("build task-list body for key %q query %+v: %v", key, query, err)
		return TaskListBody{}, errors.Wrapf(ctx, err, "build task-list body")
	}
	identity, err := c.encode(responses)
	if err != nil {
		glog.Errorf("encode task-list body for key %q query %+v: %v", key, query, err)
		return TaskListBody{}, errors.Wrapf(ctx, err, "build task-list body")
	}
	gzipped, err := gzipBody(identity)
	if err != nil {
		glog.Errorf("gzip task-list body for key %q query %+v: %v", key, query, err)
		return TaskListBody{}, errors.Wrapf(ctx, err, "build task-list body")
	}
	body := TaskListBody{Identity: identity, Gzipped: gzipped}

	// The boundary is computed over the snapshot's pre-filter rows, not the
	// projected responses: the row whose entry instant matters is precisely the
	// one visibleRow drops, so it is absent from the responses. The `now` here
	// is read slightly earlier than the one ListTasks reads internally, so the
	// only possible effect is a conservative (early) rebuild, never a stale
	// serve.
	rows, err := c.rows(ctx, query)
	if err != nil {
		glog.Errorf("read task-list rows for key %q query %+v: %v", key, query, err)
		return TaskListBody{}, errors.Wrapf(ctx, err, "build task-list body")
	}
	expiresAt := taskBodyBoundary(rows, now, query.UpcomingHours)

	// Re-read the signals the build started from: a signal that moved while the
	// build ran leaves the entry unheld and the next read rebuilds.
	current := c.signals(selected)
	c.mu.Lock()
	if slices.Equal(current, signals) {
		c.storeLocked(key, &taskBodyEntry{body: body, expiresAt: expiresAt, signals: current})
	}
	c.mu.Unlock()

	return body, nil
}

// touchLocked marks the entry most recently used.
func (c *taskBodyCache) touchLocked(e *taskBodyEntry) {
	c.lastUsedSeq++
	e.lastUsed = c.lastUsedSeq
}

// storeLocked holds the entry, evicting the least recently used held body first
// when the cache is at its cap.
func (c *taskBodyCache) storeLocked(key string, e *taskBodyEntry) {
	c.lastUsedSeq++
	e.lastUsed = c.lastUsedSeq
	c.entries[key] = e
	for len(c.entries) > taskBodyCacheMaxEntries {
		var oldestKey string
		var oldest uint64
		first := true
		for k, entry := range c.entries {
			if first || entry.lastUsed < oldest {
				oldestKey, oldest, first = k, entry.lastUsed, false
			}
		}
		delete(c.entries, oldestKey)
	}
}

// taskBodyBoundary returns the earliest instant strictly after now at which a
// clock-derived field of the built body can change, or the zero time when none
// can. The clock-derived fields are upcoming, recently_completed and the
// recently-completed phase override; they are the only places the request's now
// enters the response.
//
// It is computed over the snapshot's pre-filter rows — the rows the visibility
// filter has not yet dropped — because a deferred row beyond now+upcoming_hours
// is dropped by visibleRow and is precisely the row whose entry instant matters.
// Each row's dates and status are read from its item.
//
// It is derived from those rows and the request's windows — never from a fixed
// timer — so it moves with the data and with the request's upcoming_hours.
func taskBodyBoundary(rows []taskSnapshotRow, now time.Time, upcomingHours int) time.Time {
	var earliest time.Time
	consider := func(candidate time.Time) {
		if !candidate.After(now) {
			return
		}
		if earliest.IsZero() || candidate.Before(earliest) {
			earliest = candidate
		}
	}
	for _, row := range rows {
		if row.item.Status == "completed" {
			completedAt := parseFlexibleDate(row.item.CompletedDate)
			if completedAt == nil {
				if modifiedAt := parseDateTime(row.item.ModifiedDate); modifiedAt != nil {
					value := modifiedAt.Time()
					completedAt = &value
				}
			}
			if completedAt == nil {
				continue
			}
			consider(completedAt.Add(LookbackHours * time.Hour))
			continue
		}
		if row.item.DeferDate == "" {
			continue
		}
		deferAt, ok := parseDeferDate(row.item.DeferDate)
		if !ok {
			continue
		}
		consider(deferAt)
		consider(deferAt.Add(-time.Duration(upcomingHours) * time.Hour))
	}
	return earliest
}

// taskBodyKey is a canonical key over every query field that changes the body,
// including upcoming_hours and session_live. It must distinguish repeated values
// and their order, because selectVaults preserves the requested order and the
// body follows it.
func taskBodyKey(query TaskQuery) string {
	return fmt.Sprintf("%q|%q|%q|%q|%q|%d|%t",
		query.Vaults,
		query.Statuses,
		query.Phases,
		query.Assignees,
		query.Goals,
		query.UpcomingHours,
		query.SessionLive,
	)
}

// encodeTaskList marshals the projected rows exactly as the handler's writeJSON
// did: encoding/json with SetEscapeHTML(false) and the encoder's trailing
// newline trimmed. The bytes must match the handler's old output byte-for-byte,
// so the decoded body is unchanged.
func encodeTaskList(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// gzipBody compresses the identity body. It is the only place the board
// compresses a task-list body.
func gzipBody(identity []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(identity); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
