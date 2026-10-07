// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mutations

import (
	"context"
	"fmt"
	"strings"
	"time"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/cleanup"
	"github.com/bborbe/vault-ui/pkg/sessionresolver"
	"github.com/bborbe/vault-ui/pkg/terminate"
	"github.com/bborbe/vault-ui/pkg/vaultconfig"
)

// taskNotFound is the Python FileNotFoundError text for a missing task.
func taskNotFound(taskID string) string { return "Task not found: " + taskID }

// RunTask starts a Claude session for a task, mirroring run_task.
func (s *service) RunTask(ctx context.Context, vault, taskID string) (api.SessionResponse, error) {
	cfg, err := s.deps.Config.Load(ctx)
	if err != nil {
		return api.SessionResponse{}, err
	}
	launching := s.launchCount(ctx)
	if launching >= cfg.MaxConcurrentSessions {
		return api.SessionResponse{}, newHTTPError(
			429,
			fmt.Sprintf("%d sessions starting, cap %d", launching, cfg.MaxConcurrentSessions),
		)
	}
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return api.SessionResponse{}, err
	}
	if !ok {
		return api.SessionResponse{}, newHTTPError(500, unknownVault(vault))
	}
	defer s.markVaultDirty(resolved)
	set := s.opsForVault(resolved)
	task, err := set.Show.Execute(ctx, resolved.Path, resolved.Name, taskID)
	if err != nil {
		return api.SessionResponse{}, newHTTPError(404, taskNotFound(taskID))
	}
	if task.Phase == "todo" {
		if _, approveErr := set.Approve.Execute(
			ctx, resolved.Path, taskID, resolved.Name, "operator", "", cfg.CurrentUser,
		); approveErr != nil {
			return api.SessionResponse{}, newHTTPError(500, approveErr.Error())
		}
	}
	s.deps.Launch.Begin(vault, taskID, "task")
	if err := set.FrontmatterSet.Execute(
		ctx, resolved.Path, taskID, "claude_session_started", sessionStartedMarker(), "", "", "", false,
	); err != nil {
		s.deps.Launch.Finish(vault, taskID)
		return api.SessionResponse{}, newHTTPError(500, err.Error())
	}
	result, workErr := set.WorkOn.Execute(
		ctx, resolved.Path, taskID, "", resolved.Name, false, resolved.SessionProjectDir,
		cliVault(resolved),
	)
	if workErr != nil {
		_ = set.FrontmatterClear.Execute(ctx, resolved.Path, taskID, "claude_session_started")
		s.deps.Launch.Finish(vault, taskID)
		if s.deps.Launch.WasTakenOver(vault, taskID) {
			return api.SessionResponse{}, newHTTPError(
				409,
				"Launch ended by take-over from the wall — "+
					"resume the session from the take-over modal",
			)
		}
		return api.SessionResponse{}, newHTTPError(500, workErr.Error())
	}
	s.deps.Launch.Finish(vault, taskID)
	_ = set.FrontmatterClear.Execute(ctx, resolved.Path, taskID, "claude_session_started")
	s.deps.Cache.Invalidate(vault, taskID)
	sessionID := result.SessionID
	if sessionID == "" {
		return api.SessionResponse{}, newHTTPError(
			500, "vault-cli work-on did not start a claude session: no warnings reported",
		)
	}
	return sessionResponse(resolved, sessionID, task.Name), nil
}

// JumpTask activates the WezTerm pane a live task's session runs in, mirroring
// jump_to_task. sameOrigin carries the handler's same-origin verdict.
func (s *service) JumpTask(ctx context.Context, vault, taskID string, sameOrigin bool) error {
	if !sameOrigin {
		return newHTTPError(403, "cross-origin jump request rejected")
	}
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return err
	}
	if !ok {
		return newHTTPError(500, unknownVault(vault))
	}
	task, showErr := s.opsForVault(resolved).Show.Execute(ctx, resolved.Path, resolved.Name, taskID)
	if showErr != nil {
		return newHTTPError(404, taskNotFound(taskID))
	}
	sessionID := task.ClaudeSessionID
	if sessionID == "" {
		return newHTTPError(409, "no session to jump to")
	}
	paneID, found := s.deps.Pane.Resolve(ctx, sessionID)
	if !found {
		return newHTTPError(409, "no pane resolves for this session")
	}
	token, ok := s.deps.Jump.ReadToken()
	if !ok {
		return newHTTPError(503, "jump credential unreadable")
	}
	if jumpErr := s.deps.Jump.Perform(ctx, paneID, token); jumpErr != nil {
		return newHTTPError(502, "jump server unreachable")
	}
	return nil
}

// TakeOverTask terminates a live or starting task session and returns the
// resume command, mirroring take_over_task.
func (s *service) TakeOverTask(
	ctx context.Context, vault, taskID string,
) (api.SessionResponse, error) {
	if guard := requireSafeID(taskID, "task_id"); guard != nil {
		return api.SessionResponse{}, guard
	}
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return api.SessionResponse{}, err
	}
	if !ok {
		return api.SessionResponse{}, newHTTPError(500, unknownVault(vault))
	}
	defer s.markVaultDirty(resolved)
	set := s.opsForVault(resolved)
	task, showErr := set.Show.Execute(ctx, resolved.Path, resolved.Name, taskID)
	if showErr != nil {
		return api.SessionResponse{}, newHTTPError(404, taskNotFound(taskID))
	}
	sessionID := task.ClaudeSessionID
	terminated := false
	if s.startingMarker(vault, taskID, "") != "" {
		var resolvedID string
		resolvedID, terminated = s.terminateLaunch(ctx, sessionID, task.Name)
		if resolvedID != "" {
			sessionID = resolvedID
		}
		if err := s.clearStartingMarker(ctx, resolved, set, vault, taskID, false); err != nil {
			return api.SessionResponse{}, err
		}
		if sessionID != "" {
			s.bindSessionID(ctx, resolved, set, vault, taskID, false, sessionID)
		}
		if sessionID == "" {
			return api.SessionResponse{}, newHTTPError(
				400,
				"Task has no Claude session to resume: "+taskID+
					" (launch marker cleared — the card is back to Start)",
			)
		}
	} else {
		if sessionID == "" {
			return api.SessionResponse{}, newHTTPError(
				400, "Task has no Claude session to take over: "+taskID,
			)
		}
		terminated = s.terminateResumed(ctx, sessionID)
	}
	response := sessionResponse(resolved, sessionID, task.Name)
	response.Terminated = &terminated
	return response, nil
}

// terminateLaunch SIGTERMs the in-flight launch process of a Starting card.
func (s *service) terminateLaunch(
	ctx context.Context, sessionID, itemName string,
) (string, bool) {
	return terminate.TerminateLaunchProcess(ctx, s.deps.Scanner, s.deps.Signaler, sessionID, itemName)
}

// terminateResumed SIGTERMs the live claude process pinning sessionID.
func (s *service) terminateResumed(ctx context.Context, sessionID string) bool {
	return terminate.TerminateResumedSession(ctx, s.deps.Scanner, s.deps.Signaler, sessionID)
}

// clearStartingMarker finishes the launch record, flags the take-over, and
// clears the durable claude_session_started marker.
func (s *service) clearStartingMarker(
	ctx context.Context,
	vaultCfg vaultconfig.Vault,
	set vaultui.OpSet,
	vault, itemID string,
	isGoal bool,
) error {
	s.deps.Launch.Finish(vault, itemID)
	s.deps.Launch.MarkTakenOver(vault, itemID)
	// A failed clear never fails the take-over: the cleanup sweep converges the
	// file within one pass.
	if isGoal {
		_ = set.GoalClear.Execute(ctx, vaultCfg.Path, itemID, "claude_session_started")
	} else {
		_ = set.FrontmatterClear.Execute(ctx, vaultCfg.Path, itemID, "claude_session_started")
	}
	s.deps.Cache.Invalidate(vault, itemID)
	return nil
}

// bindSessionID watches the session-id field for the SIGTERMed launcher's
// compensating clear, re-writing the id whenever it goes missing. It is bounded
// and never fails the take-over.
func (s *service) bindSessionID(
	ctx context.Context,
	vaultCfg vaultconfig.Vault,
	set vaultui.OpSet,
	vault, itemID string,
	isGoal bool,
	sessionID string,
) {
	const polls = 10
	const stableReads = 2
	sawClear := false
	stable := 0
	for i := 0; i < polls; i++ {
		current, err := s.readBoundSessionID(ctx, set, vaultCfg, itemID, isGoal)
		if err != nil {
			return
		}
		if current == sessionID {
			stable++
			if sawClear && stable >= stableReads {
				return
			}
		} else {
			sawClear = true
			stable = 0
			if isGoal {
				_ = set.GoalSet.Execute(
					ctx, vaultCfg.Path, itemID, "claude_session_id", sessionID, "", "",
				)
			} else {
				_ = set.FrontmatterSet.Execute(
					ctx, vaultCfg.Path, itemID, "claude_session_id", sessionID, "", "", "", false,
				)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// readBoundSessionID re-reads an item's claude_session_id.
func (s *service) readBoundSessionID(
	ctx context.Context, set vaultui.OpSet, vaultCfg vaultconfig.Vault, itemID string, isGoal bool,
) (string, error) {
	if isGoal {
		goal, found, err := s.findGoal(ctx, vaultCfg, itemID)
		if err != nil {
			return "", err
		}
		if !found {
			return "", nil
		}
		return goal.ClaudeSessionID, nil
	}
	task, err := set.Show.Execute(ctx, vaultCfg.Path, vaultCfg.Name, itemID)
	if err != nil {
		return "", err
	}
	return task.ClaudeSessionID, nil
}

// ExecuteTaskCommand runs a task lifecycle command, mirroring
// execute_slash_command.
func (s *service) ExecuteTaskCommand(
	ctx context.Context, vault, taskID string, req api.ExecuteCommandRequest,
) (api.SessionResponse, error) {
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return api.SessionResponse{}, err
	}
	if !ok {
		return api.SessionResponse{}, newHTTPError(500, unknownVault(vault))
	}
	defer s.markVaultDirty(resolved)
	set := s.opsForVault(resolved)
	task, showErr := set.Show.Execute(ctx, resolved.Path, resolved.Name, taskID)
	if showErr != nil {
		return api.SessionResponse{}, newHTTPError(404, taskNotFound(taskID))
	}
	if req.Command == "defer-task" || req.Command == "complete-task" {
		return s.taskFastPath(ctx, resolved, set, vault, taskID, task.Name, req)
	}
	if req.Command != "work-on-task" && req.Command != "create-task" {
		return api.SessionResponse{}, newHTTPError(400, "Unknown command: "+req.Command)
	}
	result, workErr := set.WorkOn.Execute(
		ctx, resolved.Path, taskID, "", resolved.Name, false, resolved.SessionProjectDir,
		cliVault(resolved),
	)
	if workErr != nil {
		return api.SessionResponse{}, newHTTPError(500, workErr.Error())
	}
	s.deps.Cache.Invalidate(vault, taskID)
	if result.SessionID == "" {
		return api.SessionResponse{}, newHTTPError(
			500, "vault-cli work-on did not start a claude session: no warnings reported",
		)
	}
	return sessionResponse(resolved, result.SessionID, task.Name), nil
}

// taskFastPath runs the vault-cli defer/complete fast path in-process, then
// returns the same SessionResponse the Python subprocess path returns.
func (s *service) taskFastPath(
	ctx context.Context,
	resolved vaultconfig.Vault,
	set vaultui.OpSet,
	vault, taskID, taskTitle string,
	req api.ExecuteCommandRequest,
) (api.SessionResponse, error) {
	var (
		commandStr string
		runErr     error
	)
	if req.Command == "defer-task" {
		tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
		commandStr = strings.Join([]string{
			resolved.VaultCLIPath, "task", "defer", taskID, tomorrow,
			"--vault", strings.ToLower(resolved.Name),
		}, " ")
		_, runErr = set.Defer.Execute(ctx, resolved.Path, taskID, tomorrow, resolved.Name)
	} else {
		reason, gate, closeErr := closeoutArgs("completed", req.Reason, req.GateSuccessor)
		if closeErr != nil {
			return api.SessionResponse{}, closeErr
		}
		args := []string{
			resolved.VaultCLIPath, "task", "complete", taskID,
			"--vault", strings.ToLower(resolved.Name),
		}
		if reason != "" {
			args = append(args, "--reason", reason, "--gate-successor", gate)
		}
		commandStr = strings.Join(args, " ")
		_, runErr = set.Complete.Execute(
			ctx, resolved.Path, taskID, resolved.Name, false, reason, gate,
		)
	}
	if runErr != nil {
		return api.SessionResponse{}, newHTTPError(500, runErr.Error())
	}
	s.deps.Cache.Invalidate(vault, taskID)
	// Mark before publishing so a client reacting to the frame never re-fetches
	// stale data; the deferred mark still covers error returns after a partial
	// write, and the extra mark costs at most one extra rebuild.
	s.markVaultDirty(resolved)
	s.deps.Publisher.PublishTaskUpdated(ctx, vault, taskID)
	stdout := ""
	success := true
	return api.SessionResponse{
		SessionID:  "",
		Command:    commandStr,
		WorkingDir: resolved.Path,
		TaskTitle:  taskTitle,
		Response:   &stdout,
		Success:    &success,
	}, nil
}

// sessionConflict is the frozen 409 body for overwriting a different valid
// session UUID.
func sessionConflict(taskID, current, storedValue string) *HTTPError {
	if current != "" && sessionresolver.IsUUID(current) && current != storedValue {
		return newHTTPError(
			409,
			"Task "+taskID+" already holds session "+current+"; refusing to overwrite with "+
				storedValue+". Call DELETE /api/tasks/"+taskID+"/session to release it first.",
		)
	}
	return nil
}

// AssignTaskToMe queues a task's assignee write to the configured current user.
// The route answers 202 before the vault is written.
func (s *service) AssignTaskToMe(
	ctx context.Context, vault, taskID string,
) (api.AssignResponse, error) {
	cfg, err := s.deps.Config.Load(ctx)
	if err != nil {
		return api.AssignResponse{}, err
	}
	if cfg.CurrentUser == "" {
		return api.AssignResponse{}, newHTTPError(
			400, "current_user is not configured; cannot assign task",
		)
	}
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return api.AssignResponse{}, err
	}
	if !ok {
		return api.AssignResponse{}, newHTTPError(404, unknownVault(vault))
	}
	// The task-existence read stays on the request path so the frozen 404 body
	// (parity case unknown-task) answers synchronously. No vault-cli write, no
	// Cache.Invalidate, no MarkDirty and no frame happens here.
	if _, showErr := s.opsForVault(resolved).Show.Execute(
		ctx, resolved.Path, resolved.Name, taskID,
	); showErr != nil {
		return api.AssignResponse{}, newHTTPError(404, taskNotFound(taskID))
	}
	assignee := cfg.CurrentUser
	write := func(ctx context.Context) error {
		if setErr := s.opsForVault(resolved).FrontmatterSet.Execute(
			ctx, resolved.Path, taskID, "assignee", assignee, "", "", "", false,
		); setErr != nil {
			return newHTTPError(500, setErr.Error())
		}
		return nil
	}
	if err := s.enqueueWrite(
		ctx, resolved, "task", taskID, write, s.taskWritten(resolved, taskID),
	); err != nil {
		return api.AssignResponse{}, err
	}
	return api.AssignResponse{Status: "success", TaskID: taskID, Assignee: assignee}, nil
}

// UpdateTaskPhase queues a task's phase update, then its status to match. The
// route answers 202 before the vault is written.
//
// The one exception is the todo → planning move, which is the operator's
// approval and has its own command: it runs set.Approve instead of writing
// phase and status by hand.
func (s *service) UpdateTaskPhase(
	ctx context.Context, vault, taskID string, req api.UpdatePhaseRequest,
) (api.PhaseUpdateResponse, error) {
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return api.PhaseUpdateResponse{}, err
	}
	if !ok {
		return api.PhaseUpdateResponse{}, newHTTPError(400, unknownVault(vault))
	}
	if guard := s.approveOwnerGuard(ctx, resolved, taskID, req.Phase); guard != nil {
		return api.PhaseUpdateResponse{}, guard
	}
	write := func(ctx context.Context) error {
		set := s.opsForVault(resolved)
		task, showErr := set.Show.Execute(ctx, resolved.Path, resolved.Name, taskID)
		if showErr == nil && req.Phase == "planning" && task.Phase == "todo" {
			return s.approveTask(ctx, resolved, set, taskID)
		}
		if setErr := set.FrontmatterSet.Execute(
			ctx, resolved.Path, taskID, "phase", req.Phase, "", "", "", false,
		); setErr != nil {
			return newHTTPError(500, setErr.Error())
		}
		newStatus := ""
		if req.Phase == "done" {
			newStatus = "completed"
		} else {
			currentStatus := ""
			if showErr == nil {
				currentStatus = task.Status
			}
			if currentStatus != "hold" {
				newStatus = "in_progress"
			}
		}
		if newStatus != "" {
			if setErr := set.FrontmatterSet.Execute(
				ctx, resolved.Path, taskID, "status", newStatus, "", "", "", false,
			); setErr != nil {
				return newHTTPError(500, setErr.Error())
			}
		}
		return nil
	}
	if err := s.enqueueWrite(
		ctx, resolved, "task", taskID, write, s.taskWritten(resolved, taskID),
	); err != nil {
		return api.PhaseUpdateResponse{}, err
	}
	return api.PhaseUpdateResponse{Status: "success", TaskID: taskID, Phase: req.Phase}, nil
}

// approveTask runs the operator's approval for a task waiting in the inbox,
// with the same seven arguments RunTask passes. Approve owns all four keys in
// one write — phase: planning, status: next, approved_by and approved_at — so
// this branch writes neither phase nor status itself: setting the phase
// alongside it would race the approval's own write, and the caller's status
// logic would overwrite "next" with "in_progress", which is the write that
// removes a freshly approved row from the ready-to-start bucket.
//
// A todo task that already carries an approval record is refused by the
// operation; that case is not worth a request-path read, so it surfaces as an
// asynchronous write_failed frame.
func (s *service) approveTask(
	ctx context.Context,
	resolved vaultconfig.Vault,
	set vaultui.OpSet,
	taskID string,
) error {
	cfg, err := s.deps.Config.Load(ctx)
	if err != nil {
		return newHTTPError(500, err.Error())
	}
	if _, approveErr := set.Approve.Execute(
		ctx, resolved.Path, taskID, resolved.Name, "operator", "", cfg.CurrentUser,
	); approveErr != nil {
		return newHTTPError(500, approveErr.Error())
	}
	return nil
}

// approveOwnerGuard answers 400 when a todo → planning request names no owner,
// so the operator is told before the request is accepted instead of through an
// asynchronous write_failed frame: Approve refuses an ownerless task, and this
// closure runs on the write queue.
//
// It reads the task on the request path to decide, and applies only to the
// todo → planning move — every other transition, and every vault with no
// current_user configured, keeps today's behaviour. A read that fails (an
// unknown task) is not a guard either: that request keeps today's 202 plus the
// queued write's write_failed frame.
func (s *service) approveOwnerGuard(
	ctx context.Context,
	resolved vaultconfig.Vault,
	taskID, phase string,
) *HTTPError {
	if phase != "planning" {
		return nil
	}
	task, showErr := s.opsForVault(resolved).Show.Execute(
		ctx, resolved.Path, resolved.Name, taskID,
	)
	if showErr != nil || task.Phase != "todo" {
		return nil
	}
	cfg, err := s.deps.Config.Load(ctx)
	if err != nil {
		return nil
	}
	if task.Assignee != "" || cfg.CurrentUser != "" {
		return nil
	}
	return newHTTPError(
		400,
		"current_user is not configured and the task has no assignee; cannot approve task",
	)
}

// UpdateTaskFlag queues a set or clear of a task's flag (picked-for-today
// marker). The route answers 202 before the vault is written.
func (s *service) UpdateTaskFlag(
	ctx context.Context, vault, taskID string, req api.UpdateFlagRequest,
) (api.FlagUpdateResponse, error) {
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return api.FlagUpdateResponse{}, err
	}
	if !ok {
		return api.FlagUpdateResponse{}, newHTTPError(404, unknownVault(vault))
	}
	flag := true
	if req.Flag != nil {
		flag = *req.Flag
	}
	write := func(ctx context.Context) error {
		set := s.opsForVault(resolved)
		if flag && !selftestSkipFlagWrite() {
			if setErr := set.FrontmatterSet.Execute(
				ctx, resolved.Path, taskID, "flag", "true", "", "", "operator", false,
			); setErr != nil {
				return newHTTPError(500, setErr.Error())
			}
			return nil
		}
		if clearErr := set.FrontmatterClear.Execute(
			ctx, resolved.Path, taskID, "flag",
		); clearErr != nil {
			return newHTTPError(500, clearErr.Error())
		}
		return nil
	}
	if err := s.enqueueWrite(
		ctx, resolved, "task", taskID, write, s.taskWritten(resolved, taskID),
	); err != nil {
		return api.FlagUpdateResponse{}, err
	}
	return api.FlagUpdateResponse{Status: "success", TaskID: taskID, Flag: flag}, nil
}

// UpdateTaskStatus queues a task's status update via the task card menu. The
// route answers 202 before the vault is written.
func (s *service) UpdateTaskStatus(
	ctx context.Context, vault, taskID string, req api.UpdateStatusRequest,
) (api.StatusUpdateResponse, error) {
	if guard := requireSafeID(taskID, "task_id"); guard != nil {
		return api.StatusUpdateResponse{}, guard
	}
	reason, gate := "", ""
	if req.Status == "aborted" || req.Status == "completed" {
		var closeErr *HTTPError
		reason, gate, closeErr = closeoutArgs(req.Status, req.Reason, req.GateSuccessor)
		if closeErr != nil {
			return api.StatusUpdateResponse{}, closeErr
		}
	}
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return api.StatusUpdateResponse{}, err
	}
	if !ok {
		return api.StatusUpdateResponse{}, newHTTPError(400, unknownVault(vault))
	}
	write := func(ctx context.Context) error {
		if setErr := s.opsForVault(resolved).FrontmatterSet.Execute(
			ctx, resolved.Path, taskID, "status", req.Status, reason, gate, "", false,
		); setErr != nil {
			return newHTTPError(500, setErr.Error())
		}
		return nil
	}
	if err := s.enqueueWrite(
		ctx, resolved, "task", taskID, write, s.taskWritten(resolved, taskID),
	); err != nil {
		return api.StatusUpdateResponse{}, err
	}
	return api.StatusUpdateResponse{Status: "success", TaskID: taskID, NewStatus: req.Status}, nil
}

// ClearTaskSession queues a clear of a task's claude_session_id and started
// marker. The route answers 202 before the vault is written.
func (s *service) ClearTaskSession(
	ctx context.Context, vault, taskID string,
) (api.SessionClearResponse, error) {
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return api.SessionClearResponse{}, err
	}
	if !ok {
		return api.SessionClearResponse{}, newHTTPError(500, unknownVault(vault))
	}
	write := func(ctx context.Context) error {
		set := s.opsForVault(resolved)
		if clearErr := set.FrontmatterClear.Execute(
			ctx, resolved.Path, taskID, "claude_session_id",
		); clearErr != nil {
			return newHTTPError(404, taskNotFound(taskID))
		}
		if clearErr := set.FrontmatterClear.Execute(
			ctx, resolved.Path, taskID, "claude_session_started",
		); clearErr != nil {
			return newHTTPError(500, clearErr.Error())
		}
		return nil
	}
	if err := s.enqueueWrite(
		ctx, resolved, "task", taskID, write, s.itemWrittenSilently(resolved, taskID),
	); err != nil {
		return api.SessionClearResponse{}, err
	}
	return api.SessionClearResponse{Status: "success", TaskID: taskID}, nil
}

// SetTaskSession queues a write of a task's claude_session_id, resolving
// display names to UUIDs eagerly and refusing to overwrite a different valid
// UUID. The route answers 202 before the vault is written.
func (s *service) SetTaskSession(
	ctx context.Context, vault, taskID string, req api.UpdateSessionRequest,
) (api.SessionSetResponse, error) {
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return api.SessionSetResponse{}, err
	}
	if !ok {
		return api.SessionSetResponse{}, newHTTPError(404, unknownVault(vault))
	}
	storedValue := req.ClaudeSessionID
	if !sessionresolver.IsUUID(storedValue) {
		projectDir := cleanup.DeriveClaudeProjectDir(
			s.deps.HomeDir, resolved.Path, resolved.SessionProjectDir,
		)
		if resolvedID, found := sessionresolver.ResolveSessionID(
			ctx, storedValue, projectDir, map[string]string{},
		); found {
			storedValue = resolvedID
		}
	}
	// The different-session conflict read stays on the request path, without
	// the lock, so the frozen 409 body (parity case set-task-session-conflict)
	// answers synchronously. No vault-cli write, no Cache.Invalidate, no
	// MarkDirty and no frame happens here.
	task, showErr := s.opsForVault(resolved).Show.Execute(
		ctx, resolved.Path, resolved.Name, taskID,
	)
	if showErr != nil {
		return api.SessionSetResponse{}, newHTTPError(500, showErr.Error())
	}
	if conflict := sessionConflict(taskID, task.ClaudeSessionID, storedValue); conflict != nil {
		return api.SessionSetResponse{}, conflict
	}
	write := func(ctx context.Context) error {
		release, lockErr := s.deps.Locks.Lock(ctx, resolved.Name, taskID)
		if lockErr != nil {
			return newHTTPError(500, lockErr.Error())
		}
		defer release()
		set := s.opsForVault(resolved)
		current, readErr := set.Show.Execute(ctx, resolved.Path, resolved.Name, taskID)
		if readErr != nil {
			return newHTTPError(500, readErr.Error())
		}
		if conflict := sessionConflict(taskID, current.ClaudeSessionID, storedValue); conflict != nil {
			return conflict
		}
		if setErr := set.FrontmatterSet.Execute(
			ctx, resolved.Path, taskID, "claude_session_id", storedValue, "", "", "", false,
		); setErr != nil {
			return newHTTPError(500, setErr.Error())
		}
		return nil
	}
	if err := s.enqueueWrite(
		ctx, resolved, "task", taskID, write, s.itemWrittenSilently(resolved, taskID),
	); err != nil {
		return api.SessionSetResponse{}, err
	}
	return api.SessionSetResponse{
		Status: "success", TaskID: taskID, ClaudeSessionID: storedValue,
	}, nil
}
