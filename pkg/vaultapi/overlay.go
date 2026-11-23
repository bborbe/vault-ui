// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"sync"
	"time"

	libtime "github.com/bborbe/time"
)

// DefaultPendingTTL bounds how long a pending write overrides vault-api's rows.
// vault-api reindexes attached vaults periodically (a full rebuild takes ~37s),
// so a write it has not reflected after this long is treated as lost.
const DefaultPendingTTL = 3 * time.Minute

// Item kinds a pending write is recorded under.
const (
	KindTask  = "task"
	KindGoal  = "goal"
	KindTopic = "topic"
)

// PendingFields are the frontmatter values one successful board write set or
// cleared. A nil field was not touched by the write; a non-nil field overrides
// the cached row until vault-api reports the same value. A clear is recorded as
// the zero value ("" or false).
type PendingFields struct {
	Flag            *bool
	Assignee        *string
	Phase           *string
	Status          *string
	ClaudeSessionID *string
}

// merge layers later onto p: a field the later write touched wins.
func (p PendingFields) merge(later PendingFields) PendingFields {
	if later.Flag != nil {
		p.Flag = later.Flag
	}
	if later.Assignee != nil {
		p.Assignee = later.Assignee
	}
	if later.Phase != nil {
		p.Phase = later.Phase
	}
	if later.Status != nil {
		p.Status = later.Status
	}
	if later.ClaudeSessionID != nil {
		p.ClaudeSessionID = later.ClaudeSessionID
	}
	return p
}

func (p PendingFields) empty() bool {
	return p.Flag == nil && p.Assignee == nil && p.Phase == nil && p.Status == nil &&
		p.ClaudeSessionID == nil
}

// OverlayValues are an item's current list values, as vault-api reports them.
type OverlayValues struct {
	Flag            bool
	Assignee        string
	Phase           string
	Status          string
	ClaudeSessionID string
}

type pendingKey struct {
	vault string
	kind  string
	id    string
}

type pendingEntry struct {
	fields    PendingFields
	writtenAt time.Time
}

// Overlay holds board writes that landed on disk but that vault-api's cache
// has not reflected yet. The list path applies it so the UI's refetch after an
// update frame does not revert the card to the stale pre-write row. A field is
// dropped once vault-api reports the written value; an entry is dropped after
// its TTL. It is safe for concurrent use.
type Overlay struct {
	clock libtime.CurrentDateTimeGetter
	ttl   time.Duration

	mu      sync.Mutex
	entries map[pendingKey]pendingEntry
}

// NewOverlay returns an empty Overlay with the given clock and TTL.
func NewOverlay(clock libtime.CurrentDateTimeGetter, ttl time.Duration) *Overlay {
	return &Overlay{clock: clock, ttl: ttl, entries: map[pendingKey]pendingEntry{}}
}

// Record stores the fields one write set for (vault, kind, id), merged over
// any still-pending fields of an earlier write to the same item.
func (o *Overlay) Record(vault, kind, id string, fields PendingFields) {
	if fields.empty() {
		return
	}
	now := o.clock.Now().Time()
	key := pendingKey{vault: vault, kind: kind, id: id}
	o.mu.Lock()
	defer o.mu.Unlock()
	if existing, ok := o.entries[key]; ok && now.Sub(existing.writtenAt) < o.ttl {
		fields = existing.fields.merge(fields)
	}
	o.entries[key] = pendingEntry{fields: fields, writtenAt: now}
}

// Apply returns values with the item's pending fields laid over them. Fields
// vault-api already reports are dropped, and an expired entry is removed.
func (o *Overlay) Apply(vault, kind, id string, values OverlayValues) OverlayValues {
	key := pendingKey{vault: vault, kind: kind, id: id}
	now := o.clock.Now().Time()
	o.mu.Lock()
	defer o.mu.Unlock()
	entry, ok := o.entries[key]
	if !ok {
		return values
	}
	if now.Sub(entry.writtenAt) >= o.ttl {
		delete(o.entries, key)
		return values
	}
	f := &entry.fields
	if f.Flag != nil {
		if *f.Flag == values.Flag {
			f.Flag = nil
		} else {
			values.Flag = *f.Flag
		}
	}
	applyString(&f.Assignee, &values.Assignee)
	applyString(&f.Phase, &values.Phase)
	applyString(&f.Status, &values.Status)
	applyString(&f.ClaudeSessionID, &values.ClaudeSessionID)
	if f.empty() {
		delete(o.entries, key)
	} else {
		o.entries[key] = entry
	}
	return values
}

// Len is the number of items with pending fields.
func (o *Overlay) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.entries)
}

func applyString(pending **string, value *string) {
	if *pending == nil {
		return
	}
	if **pending == *value {
		*pending = nil
		return
	}
	*value = **pending
}
