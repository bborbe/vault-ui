// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package api holds the wire request and response types for the vault-ui HTTP
// API. The JSON field names are the contract the frozen frontend depends on and
// must match the Python models in src/vault_ui/api/models.py exactly.
//
// Nullability is load-bearing: a Python field that is None serializes as JSON
// null, so nullable fields use pointer types and nullable lists stay nil so
// encoding/json emits null rather than [].
package api

// VaultResponse is one configured vault.
type VaultResponse struct {
	Name         string `json:"name"`
	VaultPath    string `json:"vault_path"`
	TasksFolder  string `json:"tasks_folder"`
	ClaudeScript string `json:"claude_script"`
}

// AssigneesResponse is the distinct assignee set across the selected vaults.
type AssigneesResponse struct {
	Named         []string `json:"named"`
	HasUnassigned bool     `json:"has_unassigned"`
}

// TaskResponse is the wire shape of a task card.
type TaskResponse struct {
	ID                   string   `json:"id"`
	Title                string   `json:"title"`
	Status               string   `json:"status"`
	Phase                *string  `json:"phase"`
	ProjectPath          *string  `json:"project_path"`
	Description          *string  `json:"description"`
	ModifiedDate         *string  `json:"modified_date"`
	CompletedDate        *string  `json:"completed_date"`
	ObsidianURL          string   `json:"obsidian_url"`
	DeferDate            *string  `json:"defer_date"`
	PlannedDate          *string  `json:"planned_date"`
	DueDate              *string  `json:"due_date"`
	Priority             any      `json:"priority"`
	Category             *string  `json:"category"`
	Recurring            *string  `json:"recurring"`
	ClaudeSessionID      *string  `json:"claude_session_id"`
	ClaudeSessionStarted *string  `json:"claude_session_started"`
	Assignee             *string  `json:"assignee"`
	BlockedBy            []string `json:"blocked_by"`
	Blocked              bool     `json:"blocked"`
	Blockers             []string `json:"blockers"`
	Upcoming             bool     `json:"upcoming"`
	RecentlyCompleted    bool     `json:"recently_completed"`
	Vault                string   `json:"vault"`
	Goals                []string `json:"goals"`
	Flag                 bool     `json:"flag"`
	ActivityDate         *string  `json:"activity_date"`
	SessionState         *string  `json:"session_state"`
	JumpPane             *string  `json:"jump_pane"`
}

// GoalResponse is the wire shape of a goal card.
type GoalResponse struct {
	ID                   string   `json:"id"`
	Title                string   `json:"title"`
	Status               *string  `json:"status"`
	Priority             any      `json:"priority"`
	ObsidianURL          string   `json:"obsidian_url"`
	DeferDate            *string  `json:"defer_date"`
	TargetDate           *string  `json:"target_date"`
	CompletedDate        *string  `json:"completed_date"`
	Vault                string   `json:"vault"`
	ClaudeSessionID      *string  `json:"claude_session_id"`
	ClaudeSessionStarted *string  `json:"claude_session_started"`
	Assignee             *string  `json:"assignee"`
	BlockedBy            []string `json:"blocked_by"`
	Blocked              bool     `json:"blocked"`
	Blockers             []string `json:"blockers"`
	Upcoming             bool     `json:"upcoming"`
	ActivityDate         *string  `json:"activity_date"`
	SessionState         *string  `json:"session_state"`
}

// TopicResponse is the wire shape of a topic list entry.
type TopicResponse struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Vault       string `json:"vault"`
	ObsidianURL string `json:"obsidian_url"`
}

// TopicDetailResponse is the wire shape of a single topic and the work it
// tracks.
type TopicDetailResponse struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Status      string   `json:"status"`
	Vault       string   `json:"vault"`
	ObsidianURL string   `json:"obsidian_url"`
	Goals       []string `json:"goals"`
	Tasks       []string `json:"tasks"`
	Unresolved  []string `json:"unresolved"`
}

// DetailResponse is the framework-shaped error body: {"detail": "..."}.
type DetailResponse struct {
	Detail string `json:"detail"`
}
