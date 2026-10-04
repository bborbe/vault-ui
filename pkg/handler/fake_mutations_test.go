// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler_test

import (
	"context"

	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/mutations"
)

// fakeMutations is a hand-written test double for the mutation service. Each
// route has a function field so a test can assert on the exact arguments the
// handler passed; a nil field panics, surfacing an unexpected call.
type fakeMutations struct {
	runTask            func(context.Context, string, string) (api.SessionResponse, error)
	jumpTask           func(context.Context, string, string, bool) error
	takeOverTask       func(context.Context, string, string) (api.SessionResponse, error)
	runGoal            func(context.Context, string, string) (api.SessionResponse, error)
	takeOverGoal       func(context.Context, string, string) (api.SessionResponse, error)
	executeTaskCommand func(
		context.Context, string, string, api.ExecuteCommandRequest,
	) (api.SessionResponse, error)
	assignTaskToMe  func(context.Context, string, string) (api.AssignResponse, error)
	updateTaskPhase func(
		context.Context, string, string, api.UpdatePhaseRequest,
	) (api.PhaseUpdateResponse, error)
	updateTaskFlag func(
		context.Context, string, string, api.UpdateFlagRequest,
	) (api.FlagUpdateResponse, error)
	updateGoalStatus func(
		context.Context, string, string, api.UpdateStatusRequest,
	) (api.StatusUpdateResponse, error)
	executeGoalCommand func(
		context.Context, string, string, api.ExecuteCommandRequest,
	) (api.GoalCommandResponse, error)
	updateTaskStatus func(
		context.Context, string, string, api.UpdateStatusRequest,
	) (api.StatusUpdateResponse, error)
	assignGoalToMe   func(context.Context, string, string) (api.AssignResponse, error)
	clearTaskSession func(context.Context, string, string) (api.SessionClearResponse, error)
	clearGoalSession func(context.Context, string, string) (api.SessionClearResponse, error)
	setTaskSession   func(
		context.Context, string, string, api.UpdateSessionRequest,
	) (api.SessionSetResponse, error)
	reloadCache  func(context.Context, string) (api.CacheReloadResponse, error)
	reloadConfig func(context.Context) (api.ConfigReloadResponse, error)
}

var _ mutations.Service = (*fakeMutations)(nil)

func (f *fakeMutations) RunTask(
	ctx context.Context, vault, taskID string,
) (api.SessionResponse, error) {
	return f.runTask(ctx, vault, taskID)
}

func (f *fakeMutations) JumpTask(
	ctx context.Context, vault, taskID string, sameOrigin bool,
) error {
	return f.jumpTask(ctx, vault, taskID, sameOrigin)
}

func (f *fakeMutations) TakeOverTask(
	ctx context.Context, vault, taskID string,
) (api.SessionResponse, error) {
	return f.takeOverTask(ctx, vault, taskID)
}

func (f *fakeMutations) RunGoal(
	ctx context.Context, vault, goalID string,
) (api.SessionResponse, error) {
	return f.runGoal(ctx, vault, goalID)
}

func (f *fakeMutations) TakeOverGoal(
	ctx context.Context, vault, goalID string,
) (api.SessionResponse, error) {
	return f.takeOverGoal(ctx, vault, goalID)
}

func (f *fakeMutations) ExecuteTaskCommand(
	ctx context.Context, vault, taskID string, req api.ExecuteCommandRequest,
) (api.SessionResponse, error) {
	return f.executeTaskCommand(ctx, vault, taskID, req)
}

func (f *fakeMutations) AssignTaskToMe(
	ctx context.Context, vault, taskID string,
) (api.AssignResponse, error) {
	return f.assignTaskToMe(ctx, vault, taskID)
}

func (f *fakeMutations) UpdateTaskPhase(
	ctx context.Context, vault, taskID string, req api.UpdatePhaseRequest,
) (api.PhaseUpdateResponse, error) {
	return f.updateTaskPhase(ctx, vault, taskID, req)
}

func (f *fakeMutations) UpdateTaskFlag(
	ctx context.Context, vault, taskID string, req api.UpdateFlagRequest,
) (api.FlagUpdateResponse, error) {
	return f.updateTaskFlag(ctx, vault, taskID, req)
}

func (f *fakeMutations) UpdateGoalStatus(
	ctx context.Context, vault, goalID string, req api.UpdateStatusRequest,
) (api.StatusUpdateResponse, error) {
	return f.updateGoalStatus(ctx, vault, goalID, req)
}

func (f *fakeMutations) ExecuteGoalCommand(
	ctx context.Context, vault, goalID string, req api.ExecuteCommandRequest,
) (api.GoalCommandResponse, error) {
	return f.executeGoalCommand(ctx, vault, goalID, req)
}

func (f *fakeMutations) UpdateTaskStatus(
	ctx context.Context, vault, taskID string, req api.UpdateStatusRequest,
) (api.StatusUpdateResponse, error) {
	return f.updateTaskStatus(ctx, vault, taskID, req)
}

func (f *fakeMutations) AssignGoalToMe(
	ctx context.Context, vault, goalID string,
) (api.AssignResponse, error) {
	return f.assignGoalToMe(ctx, vault, goalID)
}

func (f *fakeMutations) ClearTaskSession(
	ctx context.Context, vault, taskID string,
) (api.SessionClearResponse, error) {
	return f.clearTaskSession(ctx, vault, taskID)
}

func (f *fakeMutations) ClearGoalSession(
	ctx context.Context, vault, goalID string,
) (api.SessionClearResponse, error) {
	return f.clearGoalSession(ctx, vault, goalID)
}

func (f *fakeMutations) SetTaskSession(
	ctx context.Context, vault, taskID string, req api.UpdateSessionRequest,
) (api.SessionSetResponse, error) {
	return f.setTaskSession(ctx, vault, taskID, req)
}

func (f *fakeMutations) ReloadCache(
	ctx context.Context, vault string,
) (api.CacheReloadResponse, error) {
	return f.reloadCache(ctx, vault)
}

func (f *fakeMutations) ReloadConfig(ctx context.Context) (api.ConfigReloadResponse, error) {
	return f.reloadConfig(ctx)
}
