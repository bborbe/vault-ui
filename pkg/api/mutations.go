// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package api

// UpdateFlagRequest is the body of PATCH /api/tasks/{id}/flag. The flag
// defaults to true when the body omits it, matching the Python
// `flag: bool = True` model, so a nil Flag means "true".
type UpdateFlagRequest struct {
	Flag *bool `json:"flag"`
}

// UpdatePhaseRequest is the body of PATCH /api/tasks/{id}/phase.
type UpdatePhaseRequest struct {
	Phase         string  `json:"phase"`
	Reason        *string `json:"reason"`
	GateSuccessor *string `json:"gate_successor"`
}

// UpdateStatusRequest is the body of PATCH /api/tasks/{id}/status and
// PATCH /api/goals/{id}/status. Status is enum-validated by the handler before
// any vault operation runs.
type UpdateStatusRequest struct {
	Status        string  `json:"status"`
	Reason        *string `json:"reason"`
	GateSuccessor *string `json:"gate_successor"`
}

// UpdateSessionRequest is the body of PATCH /api/tasks/{id}/session.
type UpdateSessionRequest struct {
	ClaudeSessionID string `json:"claude_session_id"`
}

// ExecuteCommandRequest is the body of the task and goal execute-command routes.
type ExecuteCommandRequest struct {
	Command       string  `json:"command"`
	Reason        *string `json:"reason"`
	GateSuccessor *string `json:"gate_successor"`
}

// SessionResponse mirrors src/vault_ui/api/models.py SessionResponse. The first
// four fields are always present; the trailing five are nullable and serialize
// as JSON null when unset (no omitempty, so every key is emitted).
type SessionResponse struct {
	SessionID       string  `json:"session_id"`
	Command         string  `json:"command"`
	WorkingDir      string  `json:"working_dir"`
	TaskTitle       string  `json:"task_title"`
	ExecutedCommand *string `json:"executed_command"`
	Success         *bool   `json:"success"`
	Error           *string `json:"error"`
	Response        *string `json:"response"`
	Terminated      *bool   `json:"terminated"`
}

// AssignResponse is the shared success payload of the assign-to-me routes.
type AssignResponse struct {
	Status   string `json:"status"`
	TaskID   string `json:"task_id,omitempty"`
	GoalID   string `json:"goal_id,omitempty"`
	Assignee string `json:"assignee"`
}

// StatusUpdateResponse is the success payload of the status routes.
type StatusUpdateResponse struct {
	Status    string `json:"status"`
	TaskID    string `json:"task_id,omitempty"`
	GoalID    string `json:"goal_id,omitempty"`
	NewStatus string `json:"new_status"`
}

// PhaseUpdateResponse is the success payload of the phase route.
type PhaseUpdateResponse struct {
	Status string `json:"status"`
	TaskID string `json:"task_id"`
	Phase  string `json:"phase"`
}

// FlagUpdateResponse is the success payload of the flag route.
type FlagUpdateResponse struct {
	Status string `json:"status"`
	TaskID string `json:"task_id"`
	Flag   bool   `json:"flag"`
}

// GoalCommandResponse is the success payload of POST /api/goals/{id}/execute-command.
type GoalCommandResponse struct {
	Status  string `json:"status"`
	GoalID  string `json:"goal_id"`
	Command string `json:"command"`
}

// SessionClearResponse is the success payload of the session DELETE routes.
type SessionClearResponse struct {
	Status string `json:"status"`
	TaskID string `json:"task_id,omitempty"`
	GoalID string `json:"goal_id,omitempty"`
}

// SessionSetResponse is the success payload of PATCH /api/tasks/{id}/session.
type SessionSetResponse struct {
	Status          string `json:"status"`
	TaskID          string `json:"task_id"`
	ClaudeSessionID string `json:"claude_session_id"`
}

// CacheReloadResponse is the body of POST /api/cache/reload.
type CacheReloadResponse struct {
	Reloaded []string       `json:"reloaded"`
	Counts   map[string]int `json:"counts"`
}

// ConfigReloadResponse is the body of POST /api/config/reload.
type ConfigReloadResponse struct {
	Vaults   []string `json:"vaults"`
	Watchers []string `json:"watchers"`
}
