// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cleanup

import (
	"context"
	"path/filepath"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// rebindEmptySessionIDs re-binds EMPTY claude_session_id values in one vault
// from task titles.
//
// A task with an EMPTY claude_session_id is invisible to the rest of the sweep,
// which filters on the field. On a git-synced shared vault a peer's cleanup can
// delete it and nothing puts it back — the board then offers "Start" for work
// already running, inviting a duplicate session.
//
// The live-process gate is the whole safety property. An empty binding is not
// always a loss: the sanctioned session reset and vault-cli work-on's
// failed-turn compensating clear both empty the field DELIBERATELY, and the
// released session's transcript keeps its custom title forever. A transcript-scan
// re-bind would resurrect exactly those releases; a running process cannot be
// resurrected from a stale file, so the live map is the honest evidence of "work
// already running".
//
// The write re-reads the task under the same per-task lock the API's
// set_task_session uses and abandons it when the binding or the assignee changed
// since the list was snapshotted. The lock ACQUISITION is bounded by
// LockAcquireTimeout; each awaited call inside the body carries its own
// SetFieldTimeout.
func (s *sweep) rebindEmptySessionIDs(
	ctx context.Context,
	vault Vault,
	ops VaultOps,
	tasks []Item,
	projectDir string,
	liveNames map[string]string,
) {
	for _, task := range tasks {
		if ctxDone(ctx) {
			return
		}
		if task.ClaudeSessionID != "" {
			continue
		}
		// The locality gate's first rule is never write a field on a task owned
		// by another user — a write prohibition, not only a clear prohibition.
		if task.Assignee != "" && task.Assignee != s.params.CurrentUser {
			glog.Infof(
				"[Cleanup] Retaining unbound task %s in vault %s: assignee %s, current user %s",
				task.ID,
				vault.Name,
				task.Assignee,
				s.params.CurrentUser,
			)
			continue
		}
		// A launch this instance started is mid-flight; its own launch path owns
		// the binding and the re-bind must not race it. State returns FINISHED as
		// well as IN_FLIGHT — skipping both is deliberate and matches the
		// marker-TTL loop.
		if _, known := s.params.LaunchRegistry.State(vault.Name, task.ID); known {
			continue
		}
		if task.Title == "" {
			continue
		}
		// The sweep lists with show-all, so the unbound complement is dominated
		// by finished work no one will resume; re-binding it is pure noise.
		if task.Status == "completed" || task.Status == "aborted" {
			continue
		}
		// Only a session running RIGHT NOW may re-bind an empty field.
		resolved, live := liveNames[task.Title]
		if !live {
			glog.V(2).Infof(
				"[Cleanup] Not re-binding task %s in vault %s: no live session carries title '%s'",
				task.ID,
				vault.Name,
				task.Title,
			)
			continue
		}
		// The live map carries no vault component, so a live session belonging to
		// a same-titled task in ANOTHER vault satisfies the gate above. The
		// transcript's location is the only per-vault evidence available.
		if !fileExists(filepath.Join(projectDir, resolved+".jsonl")) {
			glog.V(2).Infof(
				"[Cleanup] Not re-binding task %s in vault %s: session %s has no transcript"+
					" in this vault's project dir",
				task.ID,
				vault.Name,
				resolved,
			)
			continue
		}

		s.rebindUnderLock(ctx, vault, ops, task, resolved)
	}
}

// rebindUnderLock acquires the per-task lock under LockAcquireTimeout and, once
// held, re-reads the task and writes the resolved session id under
// SetFieldTimeout. A timed-out helper leaves the field untouched and the next
// sweep retries.
func (s *sweep) rebindUnderLock(
	ctx context.Context,
	vault Vault,
	ops VaultOps,
	task Item,
	resolved string,
) {
	// Acquisition is the one potentially unbounded step, and this pass runs
	// inside an unbounded background loop. Cancel the deadline the moment the
	// lock is held so it covers the WAIT only — each awaited call inside the body
	// carries its own SetFieldTimeout.
	acquireCtx, cancelAcquire := context.WithTimeout(ctx, s.params.LockAcquireTimeout)
	release, err := s.params.SessionLock.Lock(acquireCtx, vault.Name, task.ID)
	cancelAcquire()
	if err != nil {
		glog.Warningf(
			"[Cleanup] Lock acquisition for task %s in vault %s failed after %s;"+
				" skipping this task",
			task.ID,
			vault.Name,
			s.params.LockAcquireTimeout,
		)
		return
	}
	defer release()

	// The task list was snapshotted at the top of the vault block and the repair
	// loop above may have blocked for seconds per task, so emptiness at selection
	// time is not emptiness at write time. The re-read is bounded.
	var current Item
	showErr := s.bounded(ctx, func(callCtx context.Context) error {
		var readErr error
		current, readErr = ops.ShowTask(callCtx, task.ID)
		return readErr
	})
	if showErr != nil {
		if errors.Is(showErr, context.DeadlineExceeded) {
			glog.Warningf(
				"[Cleanup] Re-bind re-read for task %s in vault %s timed out after %s;"+
					" leaving claude_session_id untouched",
				task.ID,
				vault.Name,
				s.params.SetFieldTimeout,
			)
		} else {
			glog.Warningf(
				"[Cleanup] Exception re-binding session for task %s in vault %s: %v",
				task.ID,
				vault.Name,
				showErr,
			)
		}
		return
	}
	if current.ClaudeSessionID != "" {
		glog.Infof(
			"[Cleanup] Not re-binding task %s in vault %s: it now holds session %s",
			task.ID,
			vault.Name,
			current.ClaudeSessionID,
		)
		return
	}
	// The same race on the assignee, closed here and only here: set_task_session
	// performs no assignee check at all, so without this re-read a task handed to
	// another user in the seconds this loop has been blocking would still be
	// written.
	if current.Assignee != "" && current.Assignee != s.params.CurrentUser {
		glog.Infof(
			"[Cleanup] Not re-binding task %s in vault %s: it is now assigned to %s,"+
				" current user %s",
			task.ID,
			vault.Name,
			current.Assignee,
			s.params.CurrentUser,
		)
		return
	}

	if err := s.bounded(ctx, func(callCtx context.Context) error {
		return ops.SetTaskField(callCtx, task.ID, "claude_session_id", resolved)
	}); err != nil {
		// WARNING, not ERROR: the sibling display-name repair uses WARNING for the
		// same non-zero return code, and both are best-effort corrections the next
		// sweep retries.
		glog.Warningf(
			"[Cleanup] Failed to re-bind session for task %s in vault %s: %v",
			task.ID,
			vault.Name,
			err,
		)
		return
	}
	glog.Infof(
		"[Cleanup] Re-bound session '%s' to task %s (title '%s') in vault %s",
		resolved,
		task.ID,
		task.Title,
		vault.Name,
	)
}
