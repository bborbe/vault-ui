// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cleanup reproduces the Python vault_ui.cleanup five-minute sweep that
// keeps stale Claude session ids from stranding a card.
//
// Retention invariant for claude_session_id: a valid UUID is never overwritten
// with a different value and never cleared except by the explicit session reset
// or when THIS instance launched it (the launch registry records the launch) and
// its transcript file is gone — a dead local session. A UUID is never cleared
// for an assignee mismatch or a foreign transcript-missing alone: either may
// describe a session running on a peer machine and must be retained. A non-UUID
// display name may be repaired to its resolved UUID; on the task path an
// unresolvable display name is left on disk untouched (the goal path clears it,
// the one deliberate divergence). An EMPTY claude_session_id may be re-bound
// from the task title when exactly one session runs right now under that title
// AND its transcript lives in this vault's Claude project dir.
//
// Vault operations are injected through VaultOps, one instance per vault; the
// sweep never reads or writes a vault file itself and never spawns vault-cli.
// Every awaited vault operation is bounded so one stuck helper cannot freeze the
// pass.
package cleanup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bborbe/errors"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/session"
	"github.com/bborbe/vault-ui/pkg/sessionlock"
	"github.com/bborbe/vault-ui/pkg/sessionresolver"
	"github.com/bborbe/vault-ui/pkg/statuscache"
)

// Frozen values carried from the Python constants. The Go identifiers are
// idiomatic; the values (and only the values) are the contract.
const (
	// DefaultMarkerTTL is the Python _STARTING_MARKER_TTL_SECONDS (45 minutes):
	// how long a claude_session_started marker may sit without a
	// claude_session_id before the sweep treats it as orphaned.
	DefaultMarkerTTL = 45 * time.Minute
	// DefaultOrphanGrace is the Python _ORPHAN_GRACE_SECONDS (120 seconds): a
	// marker younger than this is never reconciled at startup.
	DefaultOrphanGrace = 120 * time.Second
	// DefaultSetFieldTimeout bounds each awaited vault operation
	// (_SET_FIELD_TIMEOUT_SECONDS).
	DefaultSetFieldTimeout = 10 * time.Second
	// DefaultLockAcquireTimeout bounds the wait to ACQUIRE the per-task lock
	// (_LOCK_ACQUIRE_TIMEOUT_SECONDS).
	DefaultLockAcquireTimeout = 10 * time.Second
	// DefaultCleanupInterval is the Python _CLEANUP_INTERVAL_SECONDS (five
	// minutes).
	DefaultCleanupInterval = 5 * time.Minute
)

// Item is the task/goal view the sweep operates on.
type Item struct {
	ID                   string
	Title                string
	Status               string
	Assignee             string
	ClaudeSessionID      string
	ClaudeSessionStarted string
}

// VaultOps is the vault-cli operation surface the sweep needs, one per vault.
// Implementations wrap vault-cli's ops constructors; the sweep never reads or
// writes a vault file itself.
type VaultOps interface {
	ListTasks(ctx context.Context) ([]Item, error)
	ListGoals(ctx context.Context) ([]Item, error)
	ShowTask(ctx context.Context, itemID string) (Item, error)
	SetTaskField(ctx context.Context, itemID, key, value string) error
	ClearTaskField(ctx context.Context, itemID, key string) error
	SetGoalField(ctx context.Context, itemID, key, value string) error
	ClearGoalField(ctx context.Context, itemID, key string) error
}

// VaultOpsFactory builds the ops for one vault.
type VaultOpsFactory func(vault Vault) VaultOps

// Vault is the per-vault config the sweep iterates.
type Vault struct {
	Name              string
	Path              string
	TasksFolder       string
	SessionProjectDir string
}

// SweepParams carries every injectable dependency and every frozen value. None
// of the durations is read from a wall clock inside the logic.
type SweepParams struct {
	Vaults             []Vault
	OpsFor             VaultOpsFactory
	HomeDir            string
	CurrentUser        string
	LaunchRegistry     launchregistry.Registry
	SessionLock        sessionlock.Registry
	StatusCache        statuscache.Cache
	LiveNames          func(ctx context.Context) map[string]string
	ProcessScanner     session.ProcessScanner
	Now                libtime.DateTime
	MarkerTTL          time.Duration
	OrphanGrace        time.Duration
	SetFieldTimeout    time.Duration
	LockAcquireTimeout time.Duration
	CleanupInterval    time.Duration
}

// Sweep is the five-minute cleanup pass.
type Sweep interface {
	// Run executes one cleanup pass and returns the number of session ids/markers
	// cleared. A re-bind is not a clear and does not count.
	Run(ctx context.Context) (int, error)
	// RunLoop runs Run every CleanupInterval until ctx is cancelled (the port of
	// run_cleanup_loop).
	RunLoop(ctx context.Context) error
	// ReconcileOrphanedMarkers clears claude_session_started markers whose launch
	// this host no longer has (startup reconciliation) and returns the count.
	ReconcileOrphanedMarkers(ctx context.Context) (int, error)
}

// NewSweep creates a Sweep from the given params.
func NewSweep(params SweepParams) Sweep {
	return &sweep{params: params}
}

type sweep struct {
	params SweepParams
}

// Run executes one cleanup pass across every vault. A vault-level failure is
// caught and logged so it cannot abort the remaining vaults; the returned error
// is non-nil only when ctx is cancelled.
func (s *sweep) Run(ctx context.Context) (int, error) {
	// The live name -> session-id map, computed ONCE per sweep: it is a cached
	// ps scan, and the re-bind pass asks it about every unbound task.
	var liveNames map[string]string
	if s.params.LiveNames != nil {
		liveNames = s.params.LiveNames(ctx)
	}

	cleared := 0
	for _, vault := range s.params.Vaults {
		if err := ctx.Err(); err != nil {
			return cleared, errors.Wrap(ctx, err, "cleanup cancelled")
		}
		cleared += s.runVault(ctx, vault, liveNames)
	}

	glog.Infof("[Cleanup] registry size=%d", s.params.LaunchRegistry.Size())
	glog.Infof("[Cleanup] Pass complete: cleared %d stale session(s)", cleared)
	return cleared, nil
}

// RunLoop runs Run once immediately and then every CleanupInterval until ctx is
// cancelled. The port of run_cleanup_loop.
func (s *sweep) RunLoop(ctx context.Context) error {
	glog.Infof("[Cleanup] Starting cleanup loop")
	for {
		if _, err := s.Run(ctx); err != nil {
			if ctx.Err() != nil {
				glog.Infof("[Cleanup] Cleanup loop cancelled")
				return nil
			}
			glog.Errorf("[Cleanup] Unexpected error in cleanup pass: %v", err)
		}
		timer := time.NewTimer(s.params.CleanupInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			glog.Infof("[Cleanup] Cleanup loop cancelled during sleep")
			return nil
		case <-timer.C:
		}
	}
}

// runVault runs the task pass, the re-bind pass, the task marker passes and the
// goal pass for one vault. A vault-level list failure is caught and logged; the
// goal pass carries its own catch so a goal-list failure never aborts the task
// pass that already completed.
func (s *sweep) runVault(ctx context.Context, vault Vault, liveNames map[string]string) int {
	ops := s.params.OpsFor(vault)

	tasks, err := s.listTasks(ctx, ops)
	if err != nil {
		glog.Errorf("[Cleanup] Exception processing vault %s: %v", vault.Name, err)
		return 0
	}

	projectDir := DeriveClaudeProjectDir(s.params.HomeDir, vault.Path, vault.SessionProjectDir)

	cleared := 0
	cleared += s.clearStaleTaskSessions(ctx, vault, ops, tasks, projectDir, liveNames)
	// Every branch above operates on tasks that carry a session id, so a task
	// with an EMPTY claude_session_id is invisible to the rest of the sweep.
	s.rebindEmptySessionIDs(ctx, vault, ops, tasks, projectDir, liveNames)
	cleared += s.clearOrphanedTaskMarkers(ctx, vault, ops, tasks, projectDir)
	cleared += s.reclearResurrectedTaskMarkers(ctx, vault, ops)
	cleared += s.runGoalPass(ctx, vault, ops, projectDir, liveNames)
	return cleared
}

// clearStaleTaskSessions reproduces the main task pass: for each task with a
// non-empty claude_session_id, repair or retain a display name, or clear a dead
// local session (a launch this instance recorded plus an absent transcript).
func (s *sweep) clearStaleTaskSessions(
	ctx context.Context,
	vault Vault,
	ops VaultOps,
	tasks []Item,
	projectDir string,
	liveNames map[string]string,
) int {
	cleared := 0
	for _, task := range tasks {
		if ctxDone(ctx) {
			return cleared
		}
		if task.ClaudeSessionID == "" {
			continue
		}
		sessionID := task.ClaudeSessionID

		if strings.ContainsAny(sessionID, `/\`) {
			glog.Warningf(
				"[Cleanup] Skipping task %s in vault %s: session_id contains invalid chars",
				task.ID,
				vault.Name,
			)
			continue
		}

		if !sessionresolver.IsUUID(sessionID) {
			// The task path never falls through to the clear block: a repaired
			// display name is written and an unresolvable one is retained.
			_ = s.repairDisplayName(ctx, vault, ops, task, sessionID, projectDir, liveNames, false)
			continue
		}

		if !s.shouldClearUUID(vault, task, sessionID, projectDir, false) {
			continue
		}
		if s.clearItemSession(ctx, vault, ops, task, sessionID, false) {
			cleared++
		}
	}
	return cleared
}

// runGoalPass reproduces the goal pass. It carries its own catch so a goal-list
// failure never aborts the task pass.
func (s *sweep) runGoalPass(
	ctx context.Context,
	vault Vault,
	ops VaultOps,
	projectDir string,
	liveNames map[string]string,
) int {
	goals, err := s.listGoals(ctx, ops)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such file or directory") {
			glog.V(4).Infof(
				"[Cleanup] Skipping goals for vault %s: Goals directory not configured",
				vault.Name,
			)
		} else {
			glog.Errorf("[Cleanup] Exception processing goals for vault %s: %v", vault.Name, err)
		}
		return 0
	}

	cleared := 0
	cleared += s.clearStaleGoalSessions(ctx, vault, ops, goals, projectDir, liveNames)
	cleared += s.clearOrphanedGoalMarkers(ctx, vault, ops, goals, projectDir)
	cleared += s.reclearResurrectedGoalMarkers(ctx, vault, ops)
	return cleared
}

// clearStaleGoalSessions is the goal mirror of clearStaleTaskSessions, with the
// one deliberate divergence: a goal's unresolvable display name IS cleared
// (falls through to the clear block).
func (s *sweep) clearStaleGoalSessions(
	ctx context.Context,
	vault Vault,
	ops VaultOps,
	goals []Item,
	projectDir string,
	liveNames map[string]string,
) int {
	cleared := 0
	for _, goal := range goals {
		if ctxDone(ctx) {
			return cleared
		}
		if goal.ClaudeSessionID == "" {
			continue
		}
		sessionID := goal.ClaudeSessionID

		if strings.ContainsAny(sessionID, `/\`) {
			glog.Warningf(
				"[Cleanup] Skipping goal %s in vault %s: session_id contains invalid chars",
				goal.ID,
				vault.Name,
			)
			continue
		}

		if !sessionresolver.IsUUID(sessionID) {
			if !s.repairDisplayName(ctx, vault, ops, goal, sessionID, projectDir, liveNames, true) {
				continue // repaired
			}
			// Unresolvable goal display name: fall through to the clear block.
		} else {
			if !s.shouldClearUUID(vault, goal, sessionID, projectDir, true) {
				continue
			}
		}

		if s.clearItemSession(ctx, vault, ops, goal, sessionID, true) {
			cleared++
		}
	}
	return cleared
}

// repairDisplayName resolves a non-UUID display name and writes the resolved
// UUID with the kind's Set*Field. It returns true when the caller must fall
// through to the clear block: an unresolvable GOAL display name is cleared,
// while an unresolvable TASK display name is retained and never falls through.
func (s *sweep) repairDisplayName(
	ctx context.Context,
	vault Vault,
	ops VaultOps,
	item Item,
	sessionID, projectDir string,
	liveNames map[string]string,
	isGoal bool,
) bool {
	resolved, ok := sessionresolver.ResolveSessionID(ctx, sessionID, projectDir, liveNames)
	if !ok {
		if isGoal {
			glog.Infof(
				"[Cleanup] Clearing unresolved display-name session '%s' from goal %s in vault %s",
				sessionID,
				item.ID,
				vault.Name,
			)
			return true
		}
		glog.Infof(
			"[Cleanup] Retaining unresolved display-name session '%s' from task %s in vault %s",
			sessionID,
			item.ID,
			vault.Name,
		)
		return false
	}

	var err error
	if isGoal {
		err = s.bounded(ctx, func(callCtx context.Context) error {
			return ops.SetGoalField(callCtx, item.ID, "claude_session_id", resolved)
		})
	} else {
		err = s.bounded(ctx, func(callCtx context.Context) error {
			return ops.SetTaskField(callCtx, item.ID, "claude_session_id", resolved)
		})
	}
	if err != nil {
		glog.Warningf(
			"[Cleanup] Failed to set resolved session for %s %s in vault %s: %v",
			kindName(isGoal),
			item.ID,
			vault.Name,
			err,
		)
	} else {
		glog.Infof(
			"[Cleanup] Resolved session '%s' -> '%s' for %s %s in vault %s",
			sessionID,
			resolved,
			kindName(isGoal),
			item.ID,
			vault.Name,
		)
	}
	return false
}

// shouldClearUUID reproduces the shared UUID gate: a foreign assignee is
// retained FIRST, then a present transcript is retained, then a launch-registry
// record (this instance launched it and the transcript is gone) falls through to
// the clear block, and otherwise the session is retained as non-local.
func (s *sweep) shouldClearUUID(vault Vault, item Item, sessionID, projectDir string, isGoal bool) bool {
	if item.Assignee != "" && item.Assignee != s.params.CurrentUser {
		glog.Infof(
			"[Cleanup] Retaining foreign-assignee session %s on %s %s: assignee %s, current user %s",
			sessionID,
			kindName(isGoal),
			item.ID,
			item.Assignee,
			s.params.CurrentUser,
		)
		return false
	}
	if fileExists(filepath.Join(projectDir, sessionID+".jsonl")) {
		return false
	}
	if _, known := s.params.LaunchRegistry.State(vault.Name, item.ID); known {
		// This instance launched it and the transcript is gone: a dead local
		// session — fall through to the clear block.
		return true
	}
	glog.Infof(
		"[Cleanup] Retaining non-local session %s on %s %s in vault %s",
		sessionID,
		kindName(isGoal),
		item.ID,
		vault.Name,
	)
	return false
}

// clearItemSession clears claude_session_id and, in lockstep, the started
// marker. It returns whether the session-id clear succeeded (the count that Run
// reports). The marker clear is best-effort: its failure is swallowed exactly as
// the Python path swallows it.
func (s *sweep) clearItemSession(
	ctx context.Context,
	vault Vault,
	ops VaultOps,
	item Item,
	sessionID string,
	isGoal bool,
) bool {
	var clearErr error
	if isGoal {
		clearErr = s.bounded(ctx, func(callCtx context.Context) error {
			return ops.ClearGoalField(callCtx, item.ID, "claude_session_id")
		})
	} else {
		clearErr = s.bounded(ctx, func(callCtx context.Context) error {
			return ops.ClearTaskField(callCtx, item.ID, "claude_session_id")
		})
	}
	if clearErr != nil {
		glog.Errorf(
			"[Cleanup] Failed to clear session for %s %s in vault %s: %v",
			kindName(isGoal),
			item.ID,
			vault.Name,
			clearErr,
		)
		return false
	}

	glog.Infof(
		"[Cleanup] Cleared stale session %s from %s %s in vault %s",
		sessionID,
		kindName(isGoal),
		item.ID,
		vault.Name,
	)

	// The started flag is tied to the session id lifecycle. The task path gates
	// on the item's marker; the goal model has no such field, so the goal path
	// always clears it (clearing an absent frontmatter field is idempotent).
	if isGoal || item.ClaudeSessionStarted != "" {
		if isGoal {
			_ = s.bounded(ctx, func(callCtx context.Context) error {
				return ops.ClearGoalField(callCtx, item.ID, "claude_session_started")
			})
		} else {
			_ = s.bounded(ctx, func(callCtx context.Context) error {
				return ops.ClearTaskField(callCtx, item.ID, "claude_session_started")
			})
		}
	}
	return true
}

// clearOrphanedTaskMarkers reproduces the task marker TTL pass. The marker comes
// from the StatusCache, NOT from the listed item (the CLI does not emit it).
func (s *sweep) clearOrphanedTaskMarkers(
	ctx context.Context,
	vault Vault,
	ops VaultOps,
	tasks []Item,
	projectDir string,
) int {
	cleared := 0
	for _, task := range tasks {
		if ctxDone(ctx) {
			return cleared
		}
		marker, ok := s.params.StatusCache.GetSessionStarted(vault.Name, task.ID)
		if !ok || marker == "" {
			continue
		}
		// A launch the registry knows about is never cleared by this TTL loop: an
		// IN_FLIGHT record means the turn is still running (never clear,
		// regardless of age), and a FINISHED record is handled by the re-clear
		// pass below.
		if _, known := s.params.LaunchRegistry.State(vault.Name, task.ID); known {
			continue
		}
		// An id-bearing task whose session file does NOT exist is a stale session
		// the main sweep already cleared (id AND marker in lockstep) — skip the
		// redundant second clear.
		if task.ClaudeSessionID != "" {
			if !fileExists(filepath.Join(projectDir, task.ClaudeSessionID+".jsonl")) {
				continue
			}
		}
		if s.markerIsYoung(marker, s.params.MarkerTTL) {
			continue // a turn this young may still be running
		}
		if err := s.bounded(ctx, func(callCtx context.Context) error {
			return ops.ClearTaskField(callCtx, task.ID, "claude_session_started")
		}); err != nil {
			glog.Errorf(
				"[Cleanup] Failed to clear orphaned Starting marker on task %s in vault %s: %v",
				task.ID,
				vault.Name,
				err,
			)
			continue
		}
		glog.Infof(
			"[Cleanup] Cleared orphaned Starting marker from task %s in vault %s",
			task.ID,
			vault.Name,
		)
		cleared++
	}
	return cleared
}

// clearOrphanedGoalMarkers is the goal mirror of clearOrphanedTaskMarkers. The
// Goal model has no started field, so the cache is the only source.
func (s *sweep) clearOrphanedGoalMarkers(
	ctx context.Context,
	vault Vault,
	ops VaultOps,
	goals []Item,
	projectDir string,
) int {
	cleared := 0
	for _, goal := range goals {
		if ctxDone(ctx) {
			return cleared
		}
		marker, ok := s.params.StatusCache.GetSessionStarted(vault.Name, goal.ID)
		if !ok || marker == "" {
			continue
		}
		if _, known := s.params.LaunchRegistry.State(vault.Name, goal.ID); known {
			continue
		}
		if goal.ClaudeSessionID != "" {
			if !fileExists(filepath.Join(projectDir, goal.ClaudeSessionID+".jsonl")) {
				continue
			}
		}
		if s.markerIsYoung(marker, s.params.MarkerTTL) {
			continue
		}
		if err := s.bounded(ctx, func(callCtx context.Context) error {
			return ops.ClearGoalField(callCtx, goal.ID, "claude_session_started")
		}); err != nil {
			glog.Errorf(
				"[Cleanup] Failed to clear orphaned Starting marker on goal %s in vault %s: %v",
				goal.ID,
				vault.Name,
				err,
			)
			continue
		}
		glog.Infof(
			"[Cleanup] Cleared orphaned Starting marker from goal %s in vault %s",
			goal.ID,
			vault.Name,
		)
		cleared++
	}
	return cleared
}

// reclearResurrectedTaskMarkers clears a finished launch's resurrected marker
// from disk. Fires at most once per finished record: the record is evicted once
// the clear succeeds or the marker is already gone, and a failed clear is logged
// (not swallowed) so the next pass retries.
func (s *sweep) reclearResurrectedTaskMarkers(ctx context.Context, vault Vault, ops VaultOps) int {
	cleared := 0
	for _, record := range s.params.LaunchRegistry.Finished(vault.Name) {
		if ctxDone(ctx) {
			return cleared
		}
		if record.Kind != "task" {
			continue
		}
		if !s.markerPresent(vault.Name, record.ItemID) {
			s.params.LaunchRegistry.Evict(vault.Name, record.ItemID)
			continue
		}
		if err := s.bounded(ctx, func(callCtx context.Context) error {
			return ops.ClearTaskField(callCtx, record.ItemID, "claude_session_started")
		}); err != nil {
			glog.Warningf(
				"[Cleanup] Failed to clear resurrected Starting marker on task %s in vault %s: %v",
				record.ItemID,
				vault.Name,
				err,
			)
			continue // record retained -> retried on the next pass
		}
		glog.Infof(
			"[Cleanup] Cleared resurrected Starting marker from task %s in vault %s",
			record.ItemID,
			vault.Name,
		)
		cleared++
		// Evict only if the record is still FINISHED — a concurrent launch may
		// have re-begun it while the clear was awaited, and that fresh record
		// must survive.
		s.params.LaunchRegistry.EvictIfFinished(vault.Name, record.ItemID)
	}
	return cleared
}

// reclearResurrectedGoalMarkers is the goal mirror of
// reclearResurrectedTaskMarkers.
func (s *sweep) reclearResurrectedGoalMarkers(ctx context.Context, vault Vault, ops VaultOps) int {
	cleared := 0
	for _, record := range s.params.LaunchRegistry.Finished(vault.Name) {
		if ctxDone(ctx) {
			return cleared
		}
		if record.Kind != "goal" {
			continue
		}
		if !s.markerPresent(vault.Name, record.ItemID) {
			s.params.LaunchRegistry.Evict(vault.Name, record.ItemID)
			continue
		}
		if err := s.bounded(ctx, func(callCtx context.Context) error {
			return ops.ClearGoalField(callCtx, record.ItemID, "claude_session_started")
		}); err != nil {
			glog.Warningf(
				"[Cleanup] Failed to clear resurrected Starting marker on goal %s in vault %s: %v",
				record.ItemID,
				vault.Name,
				err,
			)
			continue // record retained -> retried on the next pass
		}
		glog.Infof(
			"[Cleanup] Cleared resurrected Starting marker from goal %s in vault %s",
			record.ItemID,
			vault.Name,
		)
		cleared++
		s.params.LaunchRegistry.EvictIfFinished(vault.Name, record.ItemID)
	}
	return cleared
}

// markerPresent reports whether the status cache holds a non-empty marker.
func (s *sweep) markerPresent(vaultName, itemID string) bool {
	marker, ok := s.params.StatusCache.GetSessionStarted(vaultName, itemID)
	return ok && marker != ""
}

// markerIsYoung reports whether a marker's age is below ttl. An unparseable
// marker (legacy "true") has an unknown age and is treated as expired, so it is
// never young.
func (s *sweep) markerIsYoung(marker string, ttl time.Duration) bool {
	age, ok := markerAgeSeconds(marker, s.params.Now)
	if !ok {
		return false
	}
	return age < ttl.Seconds()
}

// bounded runs fn under a SetFieldTimeout bound so one stuck helper cannot
// freeze the pass.
func (s *sweep) bounded(ctx context.Context, fn func(ctx context.Context) error) error {
	callCtx, cancel := context.WithTimeout(ctx, s.params.SetFieldTimeout)
	defer cancel()
	return fn(callCtx)
}

// listTasks lists a vault's tasks under the field timeout.
func (s *sweep) listTasks(ctx context.Context, ops VaultOps) ([]Item, error) {
	var tasks []Item
	err := s.bounded(ctx, func(callCtx context.Context) error {
		var listErr error
		tasks, listErr = ops.ListTasks(callCtx)
		return listErr
	})
	return tasks, err
}

// listGoals lists a vault's goals under the field timeout.
func (s *sweep) listGoals(ctx context.Context, ops VaultOps) ([]Item, error) {
	var goals []Item
	err := s.bounded(ctx, func(callCtx context.Context) error {
		var listErr error
		goals, listErr = ops.ListGoals(callCtx)
		return listErr
	})
	return goals, err
}

// kindName renders the item kind for a log line.
func kindName(isGoal bool) string {
	if isGoal {
		return "goal"
	}
	return "task"
}

// fileExists reports whether path exists, matching Python's Path.exists().
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ctxDone reports whether ctx has been cancelled, so a long per-item loop can
// stop promptly rather than grinding through the rest of a cancelled sweep.
func ctxDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
