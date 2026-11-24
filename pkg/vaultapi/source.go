// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/bborbe/errors"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"
)

// DefaultPollInterval is how often the source refetches vault-api.
const DefaultPollInterval = 10 * time.Second

// Source polls vault-api, caches the fetched lists per vault, and advances a
// global revision whenever any list's content changes. It is both the board's
// task/goal/topic read source and its board.IndexRevisions: vault-api has no
// push revision signal, so this poll is the freshness model.
//
// The revision is deliberately global, not per vault: the board's snapshot store
// keys on a (path, folder) page-index key, which does not carry vault-api's vault
// name, so mapping the key to a vault would need a path→name table for a
// per-vault signal that buys little. A change in any vault rebuilds every vault's
// snapshot — correct, only coarser than the page index was.
type Source struct {
	client   Client
	interval time.Duration

	onChange func()

	// refreshMu serialises whole polls (fetch + swap) across the ticker and
	// Refresh callers.
	refreshMu sync.Mutex

	mu          sync.RWMutex
	revision    uint64
	tasks       []Task
	goals       []Goal
	topics      []Topic
	loaded      bool
	unavailable bool
	lastHash    [32]byte

	// pending lays board writes vault-api has not reflected yet over the
	// cached rows the list path serves.
	pending *Overlay
}

// NewSource returns a Source polling client every DefaultPollInterval.
func NewSource(client Client) *Source {
	return &Source{
		client:   client,
		interval: DefaultPollInterval,
		pending:  NewOverlay(libtime.NewCurrentDateTime(), DefaultPendingTTL),
	}
}

// NewSourceFromData returns a Source primed with fixed data and a non-zero
// revision, without a client or a poller. It is the test seam: production uses
// NewSource + Run.
func NewSourceFromData(tasks []Task, goals []Goal, topics []Topic) *Source {
	return &Source{
		interval: DefaultPollInterval,
		tasks:    tasks,
		goals:    goals,
		topics:   topics,
		loaded:   true,
		revision: 1,
		lastHash: hashLists(tasks, goals, topics),
		pending:  NewOverlay(libtime.NewCurrentDateTime(), DefaultPendingTTL),
	}
}

// RecordPending records the fields a successful board write set on (vault,
// kind, id), so the list path keeps serving them until vault-api catches up.
// vault is the vault-api vault name, id the item's title.
func (s *Source) RecordPending(vault, kind, id string, fields PendingFields) {
	s.pending.Record(vault, kind, id, fields)
}

// SetOnChange installs the callback run after a poll observes changed content.
// The poll calls it outside its own lock, after the new lists and revision are
// visible to readers, so a callback that re-reads the board sees fresh data.
func (s *Source) SetOnChange(onChange func()) {
	s.mu.Lock()
	s.onChange = onChange
	s.mu.Unlock()
}

// Client returns the source's vault-api client. The page view reads its page
// and backlink data through it, so the factory can wire the page service from
// the same client the board polls with.
func (s *Source) Client() Client {
	return s.client
}

// BaseURL is the client's origin, for the startup log line. It is empty for a
// source built from data with no client (the test seam).
func (s *Source) BaseURL() string {
	if s.client == nil {
		return ""
	}
	return s.client.BaseURL()
}

// Run polls until ctx is done. Its first poll runs synchronously so the board
// has data as soon as the run group starts serving.
func (s *Source) Run(ctx context.Context) error {
	_ = s.poll(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_ = s.poll(ctx)
		}
	}
}

// Refresh runs one poll synchronously and returns the first fetch error. The
// mutation service calls it after a write so the next read of the cache sees the
// write instead of the pre-write rows the 10s ticker has not replaced yet. It is
// safe to call concurrently with Run: the cache is only swapped under the lock,
// and refreshMu serialises overlapping polls so a stale fetch cannot overwrite a
// newer one.
func (s *Source) Refresh(ctx context.Context) error {
	if s.client == nil {
		return nil
	}
	return s.poll(ctx)
}

// poll fetches every list and advances the revision only when the content
// changed. A failed fetch keeps the last-good cache (the board serves
// last-known rows; the unavailable state is a later step) and logs the failure.
func (s *Source) poll(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	tasks, err := s.client.ListTasks(ctx)
	if err != nil {
		glog.Warningf("vault-api poll tasks: %v", err)
		s.setUnavailable(true)
		return errors.Wrap(ctx, err, "poll tasks")
	}
	goals, err := s.client.ListGoals(ctx)
	if err != nil {
		glog.Warningf("vault-api poll goals: %v", err)
		s.setUnavailable(true)
		return errors.Wrap(ctx, err, "poll goals")
	}
	topics, err := s.client.ListTopics(ctx)
	if err != nil {
		glog.Warningf("vault-api poll topics: %v", err)
		s.setUnavailable(true)
		return errors.Wrap(ctx, err, "poll topics")
	}
	hash := hashLists(tasks, goals, topics)

	s.mu.Lock()
	changed := !s.loaded || hash != s.lastHash
	s.tasks, s.goals, s.topics = tasks, goals, topics
	s.lastHash = hash
	s.loaded = true
	s.unavailable = false
	if changed {
		s.revision++
	}
	onChange := s.onChange
	s.mu.Unlock()

	if changed && onChange != nil {
		onChange()
	}
	return nil
}

// setUnavailable records the source's reachability after a poll.
func (s *Source) setUnavailable(unavailable bool) {
	s.mu.Lock()
	s.unavailable = unavailable
	s.mu.Unlock()
}

// Unavailable reports whether the last poll failed — the board then serves the
// last-good rows and the frontend shows the "vault-api unavailable" banner.
func (s *Source) Unavailable() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.unavailable
}

// Tasks returns the cached tasks for the named vault.
func (s *Source) Tasks(vaultName string) []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return filterTasks(s.tasks, vaultName)
}

// Goals returns the cached goals for the named vault.
func (s *Source) Goals(vaultName string) []Goal {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return filterGoals(s.goals, vaultName)
}

// Topics returns the cached topics for the named vault.
func (s *Source) Topics(vaultName string) []Topic {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return filterTopics(s.topics, vaultName)
}

// Revision implements board.IndexRevisions. The global revision advances when
// any vault's list content changes, and also when the pending-write overlay
// changes: the board caches its task snapshot by this revision, so a write
// recorded on the overlay must move it or the cached pre-write rows are served.
func (s *Source) Revision(_ SnapshotKey) uint64 {
	s.mu.RLock()
	revision := s.revision
	s.mu.RUnlock()
	return revision + s.pending.Version()
}

// hashLists hashes the three lists so a change in any item advances the
// revision.
func hashLists(tasks []Task, goals []Goal, topics []Topic) [32]byte {
	payload, err := json.Marshal(struct {
		Tasks  []Task  `json:"tasks"`
		Goals  []Goal  `json:"goals"`
		Topics []Topic `json:"topics"`
	}{tasks, goals, topics})
	if err != nil {
		// The three types marshal unconditionally; an error here would be a
		// programming fault, so return a zero hash and let the caller's change
		// detection compare it (it differs from any prior non-zero hash).
		return [32]byte{}
	}
	return sha256.Sum256(payload)
}

func filterTasks(all []Task, vaultName string) []Task {
	if vaultName == "" {
		return all
	}
	out := make([]Task, 0, len(all))
	for _, t := range all {
		if matchesVault(t.Vault, vaultName) {
			out = append(out, t)
		}
	}
	return out
}

func filterGoals(all []Goal, vaultName string) []Goal {
	if vaultName == "" {
		return all
	}
	out := make([]Goal, 0, len(all))
	for _, g := range all {
		if matchesVault(g.Vault, vaultName) {
			out = append(out, g)
		}
	}
	return out
}

func filterTopics(all []Topic, vaultName string) []Topic {
	if vaultName == "" {
		return all
	}
	out := make([]Topic, 0, len(all))
	for _, t := range all {
		if matchesVault(t.Vault, vaultName) {
			out = append(out, t)
		}
	}
	return out
}

// matchesVault compares a vault-api item's vault name to the board's. vault-ui
// derives its display name by title-casing the vault-cli key (or a vault_name
// override), while vault-api reports the raw vault-cli name, so the two differ
// only by case for the common vault — compare case-insensitively.
func matchesVault(itemVault, boardVault string) bool {
	return strings.EqualFold(itemVault, boardVault)
}
