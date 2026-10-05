// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mutations

import (
	"context"
	"time"

	"github.com/bborbe/vault-ui/pkg/api"
)

// goalNotFound is the Python 404 text for a missing goal on the run route.
func goalNotFound(goalID string) string { return "Goal not found: " + goalID }

// RunGoal starts a Claude session for a goal, mirroring run_goal.
func (s *service) RunGoal(ctx context.Context, vault, goalID string) (api.SessionResponse, error) {
	if guard := requireSafeID(goalID, "goal_id"); guard != nil {
		return api.SessionResponse{}, guard
	}
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return api.SessionResponse{}, err
	}
	if !ok {
		return api.SessionResponse{}, newHTTPError(500, unknownVault(vault))
	}
	goal, found, err := s.findGoal(ctx, resolved, goalID)
	if err != nil {
		return api.SessionResponse{}, newHTTPError(500, err.Error())
	}
	if !found {
		return api.SessionResponse{}, newHTTPError(404, goalNotFound(goalID))
	}
	defer s.markVaultDirty(resolved)
	set := s.opsForVault(resolved)
	s.deps.Launch.Begin(vault, goalID, "goal")
	if err := set.GoalSet.Execute(
		ctx, resolved.Path, goalID, "claude_session_started", sessionStartedMarker(), "", "",
	); err != nil {
		s.deps.Launch.Finish(vault, goalID)
		return api.SessionResponse{}, newHTTPError(500, err.Error())
	}
	result, workErr := set.GoalWorkOn.Execute(
		ctx, resolved.Path, goalID, "", resolved.Name, false, resolved.SessionProjectDir,
		cliVault(resolved),
	)
	if workErr != nil {
		_ = set.GoalClear.Execute(ctx, resolved.Path, goalID, "claude_session_started")
		s.deps.Launch.Finish(vault, goalID)
		if s.deps.Launch.WasTakenOver(vault, goalID) {
			return api.SessionResponse{}, newHTTPError(
				409,
				"Launch ended by take-over from the wall — "+
					"resume the session from the take-over modal",
			)
		}
		return api.SessionResponse{}, newHTTPError(500, workErr.Error())
	}
	s.deps.Launch.Finish(vault, goalID)
	if setErr := set.GoalSet.Execute(
		ctx, resolved.Path, goalID, "claude_session_id", result.SessionID, "", "",
	); setErr != nil {
		return api.SessionResponse{}, newHTTPError(500, setErr.Error())
	}
	_ = set.GoalClear.Execute(ctx, resolved.Path, goalID, "claude_session_started")
	s.deps.Cache.Invalidate(vault, goalID)
	if result.SessionID == "" {
		return api.SessionResponse{}, newHTTPError(
			500, "vault-cli goal work-on did not start a claude session: no warnings reported",
		)
	}
	return sessionResponse(resolved, result.SessionID, goal.Name), nil
}

// TakeOverGoal terminates a live or starting goal session and returns the
// resume command, mirroring take_over_goal.
func (s *service) TakeOverGoal(
	ctx context.Context, vault, goalID string,
) (api.SessionResponse, error) {
	if guard := requireSafeID(goalID, "goal_id"); guard != nil {
		return api.SessionResponse{}, guard
	}
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return api.SessionResponse{}, err
	}
	if !ok {
		return api.SessionResponse{}, newHTTPError(500, unknownVault(vault))
	}
	goal, found, err := s.findGoal(ctx, resolved, goalID)
	if err != nil {
		return api.SessionResponse{}, newHTTPError(500, err.Error())
	}
	if !found {
		return api.SessionResponse{}, newHTTPError(404, goalNotFound(goalID))
	}
	defer s.markVaultDirty(resolved)
	set := s.opsForVault(resolved)
	sessionID := goal.ClaudeSessionID
	terminated := false
	if s.startingMarker(vault, goalID, "") != "" {
		var resolvedID string
		resolvedID, terminated = s.terminateLaunch(ctx, sessionID, goal.Name)
		if resolvedID != "" {
			sessionID = resolvedID
		}
		if err := s.clearStartingMarker(ctx, resolved, set, vault, goalID, true); err != nil {
			return api.SessionResponse{}, err
		}
		if sessionID != "" {
			s.bindSessionID(ctx, resolved, set, vault, goalID, true, sessionID)
		}
		if sessionID == "" {
			return api.SessionResponse{}, newHTTPError(
				400,
				"Goal has no Claude session to resume: "+goalID+
					" (launch marker cleared — the card is back to Start)",
			)
		}
	} else {
		if sessionID == "" {
			return api.SessionResponse{}, newHTTPError(
				400, "Goal has no Claude session to take over: "+goalID,
			)
		}
		terminated = s.terminateResumed(ctx, sessionID)
	}
	response := sessionResponse(resolved, sessionID, goal.Name)
	response.Terminated = &terminated
	return response, nil
}

// UpdateGoalStatus updates a goal's status (drag-and-drop on the Goals view).
func (s *service) UpdateGoalStatus(
	ctx context.Context, vault, goalID string, req api.UpdateStatusRequest,
) (api.StatusUpdateResponse, error) {
	if guard := requireSafeID(goalID, "goal_id"); guard != nil {
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
	defer s.markVaultDirty(resolved)
	set := s.opsForVault(resolved)
	if setErr := set.GoalSet.Execute(
		ctx, resolved.Path, goalID, "status", req.Status, reason, gate,
	); setErr != nil {
		return api.StatusUpdateResponse{}, newHTTPError(500, setErr.Error())
	}
	s.deps.Cache.Invalidate(vault, goalID)
	// Mark before publishing so a client reacting to the frame never re-fetches
	// stale data; the deferred mark still covers error returns after a partial
	// write, and the extra mark costs at most one extra rebuild.
	s.markVaultDirty(resolved)
	s.deps.Publisher.PublishGoalUpdated(ctx, vault, goalID)
	return api.StatusUpdateResponse{Status: "success", GoalID: goalID, NewStatus: req.Status}, nil
}

// ExecuteGoalCommand runs a goal lifecycle command (complete-goal/defer-goal).
func (s *service) ExecuteGoalCommand(
	ctx context.Context, vault, goalID string, req api.ExecuteCommandRequest,
) (api.GoalCommandResponse, error) {
	if guard := requireSafeID(goalID, "goal_id"); guard != nil {
		return api.GoalCommandResponse{}, guard
	}
	if req.Command != "complete-goal" && req.Command != "defer-goal" {
		return api.GoalCommandResponse{}, newHTTPError(400, "Unknown command: "+req.Command)
	}
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return api.GoalCommandResponse{}, err
	}
	if !ok {
		return api.GoalCommandResponse{}, newHTTPError(400, unknownVault(vault))
	}
	defer s.markVaultDirty(resolved)
	set := s.opsForVault(resolved)
	var runErr error
	if req.Command == "defer-goal" {
		tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
		_, runErr = set.GoalDefer.Execute(ctx, resolved.Path, goalID, tomorrow, resolved.Name)
	} else {
		reason, gate, closeErr := closeoutArgs("completed", req.Reason, req.GateSuccessor)
		if closeErr != nil {
			return api.GoalCommandResponse{}, closeErr
		}
		_, runErr = set.GoalComplete.Execute(
			ctx, resolved.Path, goalID, resolved.Name, false, reason, gate,
		)
	}
	if runErr != nil {
		return api.GoalCommandResponse{}, newHTTPError(500, runErr.Error())
	}
	s.deps.Cache.Invalidate(vault, goalID)
	// Mark before publishing so a client reacting to the frame never re-fetches
	// stale data; the deferred mark still covers error returns after a partial
	// write, and the extra mark costs at most one extra rebuild.
	s.markVaultDirty(resolved)
	s.deps.Publisher.PublishGoalUpdated(ctx, vault, goalID)
	return api.GoalCommandResponse{Status: "success", GoalID: goalID, Command: req.Command}, nil
}

// AssignGoalToMe sets a goal's assignee to the configured current user.
func (s *service) AssignGoalToMe(
	ctx context.Context, vault, goalID string,
) (api.AssignResponse, error) {
	if guard := requireSafeID(goalID, "goal_id"); guard != nil {
		return api.AssignResponse{}, guard
	}
	cfg, err := s.deps.Config.Load(ctx)
	if err != nil {
		return api.AssignResponse{}, err
	}
	if cfg.CurrentUser == "" {
		return api.AssignResponse{}, newHTTPError(
			400, "current_user is not configured; cannot assign goal",
		)
	}
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return api.AssignResponse{}, err
	}
	if !ok {
		return api.AssignResponse{}, newHTTPError(404, unknownVault(vault))
	}
	defer s.markVaultDirty(resolved)
	set := s.opsForVault(resolved)
	if setErr := set.GoalSet.Execute(
		ctx, resolved.Path, goalID, "assignee", cfg.CurrentUser, "", "",
	); setErr != nil {
		return api.AssignResponse{}, newHTTPError(500, setErr.Error())
	}
	s.deps.Cache.Invalidate(vault, goalID)
	// Mark before publishing so a client reacting to the frame never re-fetches
	// stale data; the deferred mark still covers error returns after a partial
	// write, and the extra mark costs at most one extra rebuild.
	s.markVaultDirty(resolved)
	s.deps.Publisher.PublishGoalUpdated(ctx, vault, goalID)
	return api.AssignResponse{Status: "success", GoalID: goalID, Assignee: cfg.CurrentUser}, nil
}

// ClearGoalSession clears a goal's claude_session_id (Reset Session).
func (s *service) ClearGoalSession(
	ctx context.Context, vault, goalID string,
) (api.SessionClearResponse, error) {
	if guard := requireSafeID(goalID, "goal_id"); guard != nil {
		return api.SessionClearResponse{}, guard
	}
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return api.SessionClearResponse{}, err
	}
	if !ok {
		return api.SessionClearResponse{}, newHTTPError(400, unknownVault(vault))
	}
	defer s.markVaultDirty(resolved)
	set := s.opsForVault(resolved)
	if clearErr := set.GoalClear.Execute(
		ctx, resolved.Path, goalID, "claude_session_id",
	); clearErr != nil {
		return api.SessionClearResponse{}, newHTTPError(500, clearErr.Error())
	}
	// Best-effort: the id clear already succeeded, so a failure here self-heals
	// on the next cleanup pass rather than failing the reset.
	_ = set.GoalClear.Execute(ctx, resolved.Path, goalID, "claude_session_started")
	s.deps.Cache.Invalidate(vault, goalID)
	// Mark before publishing so a client reacting to the frame never re-fetches
	// stale data; the deferred mark still covers error returns after a partial
	// write, and the extra mark costs at most one extra rebuild.
	s.markVaultDirty(resolved)
	s.deps.Publisher.PublishGoalUpdated(ctx, vault, goalID)
	return api.SessionClearResponse{Status: "success", GoalID: goalID}, nil
}
