// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package board holds the read-only board logic behind the vault-ui HTTP API.
// It reproduces the Python vault_ui.api.tasks read handlers: the same filters,
// the same derived fields (blocked, upcoming, recently_completed, session
// state, activity date, Obsidian deep link), and the same response values, so
// the frozen frontend renders identically.
//
// All vault access goes through vault-cli's exported Go API (a library call);
// no vault-cli subprocess is spawned.
package board

import (
	"context"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/bborbe/vault-cli/pkg/storage"

	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/statuscache"
)

// DefaultUpcomingHours is the default "upcoming" window in hours.
const DefaultUpcomingHours = 8

// LookbackHours is the "recently completed" window in hours.
const LookbackHours = 8

// Vault is a resolved vault the board reads from. Name is the display name
// (vault-ui's `vault_name`), while VaultName is the underlying vault-cli name.
type Vault struct {
	Name              string
	VaultName         string
	Path              string
	TasksFolder       string
	GoalsFolder       string
	TopicsFolder      string
	ClaudeScript      string
	SessionProjectDir string
}

// StorageConfig returns the vault-cli storage config for the vault's read
// folders.
func (v Vault) StorageConfig() *storage.Config {
	return &storage.Config{
		TasksDir:  v.TasksFolder,
		GoalsDir:  v.GoalsFolder,
		TopicsDir: v.TopicsFolder,
	}
}

// VaultsProvider returns the configured vaults.
type VaultsProvider interface {
	Vaults(ctx context.Context) ([]Vault, error)
}

// OpsProvider builds the vault-cli read operations for a vault.
type OpsProvider interface {
	List(vault Vault) ops.ListOperation
	TopicShow(vault Vault) ops.EntityShowOperation
}

// SessionSignals supplies the two per-request liveness signals that are not
// derivable from the vault: the Claude session registry ids and the live
// `--resume`/`--session-id` process ids.
type SessionSignals interface {
	RegistrySessionIDs(ctx context.Context) []string
	ResumeSessionIDs(ctx context.Context) []string
}

// PaneResolver resolves a live session id to its WezTerm pane id.
type PaneResolver interface {
	Resolve(ctx context.Context, sessionID string) (string, bool)
}

// Deps are the board's injected dependencies.
type Deps struct {
	Vaults  VaultsProvider
	Ops     OpsProvider
	Cache   statuscache.Cache
	Launch  launchregistry.Registry
	Clock   libtime.CurrentDateTimeGetter
	Signals SessionSignals
	Pane    PaneResolver
	HomeDir string
}

// Board is the read-only board service.
type Board interface {
	ListVaults(ctx context.Context) ([]api.VaultResponse, error)
	ListAssignees(ctx context.Context, vaults []string) (api.AssigneesResponse, error)
	ListTasks(ctx context.Context, query TaskQuery) ([]api.TaskResponse, error)
	ListGoals(ctx context.Context, query GoalQuery) ([]api.GoalResponse, error)
	ListTopics(ctx context.Context, vaults []string) ([]api.TopicResponse, error)
	ShowTopic(ctx context.Context, vault, topicID string) (api.TopicDetailResponse, error)
}

type board struct {
	vaults  VaultsProvider
	ops     OpsProvider
	cache   statuscache.Cache
	launch  launchregistry.Registry
	clock   libtime.CurrentDateTimeGetter
	signals SessionSignals
	pane    PaneResolver
	homeDir string
}

// New returns a Board backed by the given dependencies.
func New(deps Deps) Board {
	return &board{
		vaults:  deps.Vaults,
		ops:     deps.Ops,
		cache:   deps.Cache,
		launch:  deps.Launch,
		clock:   deps.Clock,
		signals: deps.Signals,
		pane:    deps.Pane,
		homeDir: deps.HomeDir,
	}
}

// TaskQuery is the parsed query for GET /api/tasks. Raw values keep the
// repeated/comma-split semantics of the Python `_flatten_filter` helpers.
type TaskQuery struct {
	Vaults        []string
	Statuses      []string
	Phases        []string
	Assignees     []string
	Goals         []string
	UpcomingHours int
	SessionLive   bool
}

// GoalQuery is the parsed query for GET /api/goals.
type GoalQuery struct {
	Vaults        []string
	Statuses      []string
	Assignees     []string
	UpcomingHours int
}

// UnknownVaultError reports a vault name the config does not know. Its message
// matches the Python `ValueError(f"Unknown vault: {name}")` text.
type UnknownVaultError struct {
	Vault string
}

func (e UnknownVaultError) Error() string {
	return "Unknown vault: " + e.Vault
}
