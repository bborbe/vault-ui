// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package mutations implements the write side of the vault-ui HTTP API: the
// task and goal session lifecycle, frontmatter writes, command execution, and
// the cache/config reload routes. It reproduces the Python
// vault_ui.api.tasks write handlers — the same status codes, bodies, guards,
// and vault-file side effects — so the frozen frontend behaves identically.
//
// All vault access goes through vault-cli's exported Go API (a library call);
// no vault-cli subprocess is ever spawned.
package mutations

import (
	"context"
	"strings"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/ops"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/session"
	"github.com/bborbe/vault-ui/pkg/sessionlock"
	"github.com/bborbe/vault-ui/pkg/sigterm"
	"github.com/bborbe/vault-ui/pkg/statuscache"
	"github.com/bborbe/vault-ui/pkg/vaultconfig"
)

// HTTPError carries the exact status code and {"detail": ...} body the Python
// backend returns. Handlers write it verbatim; any other error becomes a 500
// with its own message, matching the Python generic `except Exception`.
type HTTPError struct {
	Status int
	Detail string
}

func (e *HTTPError) Error() string { return e.Detail }

// newHTTPError builds an HTTPError.
func newHTTPError(status int, detail string) *HTTPError {
	return &HTTPError{Status: status, Detail: detail}
}

// EventPublisher announces a board mutation so the WebSocket layer can push a
// refresh to connected clients. Prompt 3 supplies the connection-manager-backed
// implementation; this prompt wires a no-op.
type EventPublisher interface {
	PublishTaskUpdated(ctx context.Context, vault, taskID string)
	PublishGoalUpdated(ctx context.Context, vault, goalID string)
}

// ConfigProvider resolves the merged vault-ui/vault-cli configuration. It never
// caches: a vault added to vault-cli is visible on the next request.
type ConfigProvider interface {
	Load(ctx context.Context) (*vaultconfig.Config, error)
}

// OpsFactory builds the vault-cli operation set for one vault.
type OpsFactory func(vault vaultconfig.Vault) vaultui.OpSet

// PaneResolver resolves a live session id to its WezTerm pane id.
type PaneResolver interface {
	Resolve(ctx context.Context, sessionID string) (string, bool)
}

// JumpClient reads the shared jump credential and performs the pane jump.
type JumpClient interface {
	ReadToken() (string, bool)
	Perform(ctx context.Context, paneID, token string) error
}

// Deps are the mutation service's injected dependencies.
type Deps struct {
	Config       ConfigProvider
	Ops          OpsFactory
	Cache        statuscache.Cache
	Launch       launchregistry.Registry
	Locks        sessionlock.Registry
	Publisher    EventPublisher
	Clock        libtime.CurrentDateTimeGetter
	Scanner      session.ProcessScanner
	Signaler     sigterm.Signaler
	Pane         PaneResolver
	Jump         JumpClient
	HomeDir      string
	WatcherNames func(ctx context.Context) []string
}

// Service is the mutating board service.
type Service interface {
	RunTask(ctx context.Context, vault, taskID string) (api.SessionResponse, error)
	JumpTask(ctx context.Context, vault, taskID string, sameOrigin bool) error
	TakeOverTask(ctx context.Context, vault, taskID string) (api.SessionResponse, error)
	RunGoal(ctx context.Context, vault, goalID string) (api.SessionResponse, error)
	TakeOverGoal(ctx context.Context, vault, goalID string) (api.SessionResponse, error)
	ExecuteTaskCommand(
		ctx context.Context, vault, taskID string, req api.ExecuteCommandRequest,
	) (api.SessionResponse, error)
	AssignTaskToMe(ctx context.Context, vault, taskID string) (api.AssignResponse, error)
	UpdateTaskPhase(
		ctx context.Context, vault, taskID string, req api.UpdatePhaseRequest,
	) (api.PhaseUpdateResponse, error)
	UpdateTaskFlag(
		ctx context.Context, vault, taskID string, req api.UpdateFlagRequest,
	) (api.FlagUpdateResponse, error)
	UpdateGoalStatus(
		ctx context.Context, vault, goalID string, req api.UpdateStatusRequest,
	) (api.StatusUpdateResponse, error)
	ExecuteGoalCommand(
		ctx context.Context, vault, goalID string, req api.ExecuteCommandRequest,
	) (api.GoalCommandResponse, error)
	UpdateTaskStatus(
		ctx context.Context, vault, taskID string, req api.UpdateStatusRequest,
	) (api.StatusUpdateResponse, error)
	AssignGoalToMe(ctx context.Context, vault, goalID string) (api.AssignResponse, error)
	ClearTaskSession(ctx context.Context, vault, taskID string) (api.SessionClearResponse, error)
	ClearGoalSession(ctx context.Context, vault, goalID string) (api.SessionClearResponse, error)
	SetTaskSession(
		ctx context.Context, vault, taskID string, req api.UpdateSessionRequest,
	) (api.SessionSetResponse, error)
	ReloadCache(ctx context.Context, vault string) (api.CacheReloadResponse, error)
	ReloadConfig(ctx context.Context) (api.ConfigReloadResponse, error)
}

type service struct {
	deps Deps
}

// New returns a Service backed by the given dependencies.
func New(deps Deps) Service {
	return &service{deps: deps}
}

// requireSafeID rejects an identifier beginning with '-' before any vault
// operation runs — the Python argument-injection guard.
func requireSafeID(id, label string) *HTTPError {
	if strings.HasPrefix(id, "-") {
		return newHTTPError(400, label+" must not start with '-'")
	}
	return nil
}

// closeoutArgs returns the (reason, gateSuccessor) pair a close-out write
// carries. Only an `aborted` target demands a non-empty reason; `completed`
// passes nothing, matching _closeout_extra_args.
func closeoutArgs(status string, reason, gateSuccessor *string) (string, string, *HTTPError) {
	if status != "aborted" {
		return "", "", nil
	}
	trimmed := ""
	if reason != nil {
		trimmed = strings.TrimSpace(*reason)
	}
	if trimmed == "" {
		return "", "", newHTTPError(
			400,
			"reason is required to close out a task or goal (aborted/completed)",
		)
	}
	gate := ""
	if gateSuccessor != nil {
		gate = strings.TrimSpace(*gateSuccessor)
	}
	if gate == "" {
		gate = "none"
	}
	return trimmed, gate, nil
}

// vaultByName resolves a vault by its display name, or nil when unknown.
func (s *service) vaultByName(ctx context.Context, name string) (vaultconfig.Vault, bool, error) {
	cfg, err := s.deps.Config.Load(ctx)
	if err != nil {
		return vaultconfig.Vault{}, false, err
	}
	for _, vault := range cfg.Vaults {
		if vault.Name == name {
			return vault, true, nil
		}
	}
	return vaultconfig.Vault{}, false, nil
}

// unknownVault is the Python ValueError text for an unknown vault name.
func unknownVault(name string) string { return "Unknown vault: " + name }

// opsForVault builds the vault-cli op set for a resolved vault.
func (s *service) opsForVault(vault vaultconfig.Vault) vaultui.OpSet {
	return s.deps.Ops(vault)
}

// cliVault reconstructs the vault-cli config entry the ops' Execute calls take.
func cliVault(vault vaultconfig.Vault) *config.Vault {
	return &config.Vault{
		Path:              vault.Path,
		Name:              vault.Name,
		TasksDir:          vault.TasksFolder,
		GoalsDir:          vault.GoalsFolder,
		TopicsDir:         vault.TopicsFolder,
		ClaudeScript:      vault.ClaudeScript,
		SessionProjectDir: vault.SessionProjectDir,
	}
}

// resumeCommand builds the claude --resume command, mirroring
// _build_resume_command including the `-n <title>` suffix and the optional cd
// prefix.
func resumeCommand(vault vaultconfig.Vault, sessionID, taskTitle string) string {
	script := vault.ClaudeScript
	nameSuffix := ""
	if strings.TrimSpace(taskTitle) != "" {
		nameSuffix = " -n " + shellQuote(taskTitle)
	}
	if vault.SessionProjectDir != "" {
		cwd := expandTilde(vault.SessionProjectDir)
		return `cd "` + cwd + `" && ` + script + " --resume " + sessionID + nameSuffix
	}
	return script + " --resume " + sessionID + nameSuffix
}

// sessionResponse builds the four-field base SessionResponse every session
// route returns before its route-specific fields are populated.
func sessionResponse(
	vault vaultconfig.Vault, sessionID, taskTitle string,
) api.SessionResponse {
	return api.SessionResponse{
		SessionID:  sessionID,
		Command:    resumeCommand(vault, sessionID, taskTitle),
		WorkingDir: vault.Path,
		TaskTitle:  taskTitle,
	}
}

// launchCount counts launches in flight across every configured vault — the
// Start-button admission gate.
func (s *service) launchCount(ctx context.Context) int {
	cfg, err := s.deps.Config.Load(ctx)
	if err != nil {
		return 0
	}
	total := 0
	for _, vault := range cfg.Vaults {
		set := s.opsForVault(vault)
		items, listErr := set.List.Execute(
			ctx, vault.Path, vault.Name, vault.TasksFolder, nil, true, "", "",
		)
		if listErr != nil {
			continue
		}
		for _, item := range items {
			if _, started := s.deps.Cache.GetSessionStarted(vault.Name, item.Name); !started {
				continue
			}
			if state, ok := s.deps.Launch.State(vault.Name, item.Name); ok &&
				state == launchregistry.Finished {
				continue
			}
			total++
		}
	}
	return total
}

// startingMarker reports the launch marker the board shows for an item, or ""
// when the board shows a session instead. A FINISHED registry record suppresses
// a resurrected marker.
func (s *service) startingMarker(vaultName, itemID, fileMarker string) string {
	if state, ok := s.deps.Launch.State(vaultName, itemID); ok &&
		state == launchregistry.Finished {
		return ""
	}
	if marker, ok := s.deps.Cache.GetSessionStarted(vaultName, itemID); ok && marker != "" {
		return marker
	}
	return fileMarker
}

// listGoals returns every goal in the vault, mirroring list_goals(show_all=True).
func (s *service) listGoals(
	ctx context.Context, vault vaultconfig.Vault,
) ([]ops.TaskListItem, error) {
	return s.opsForVault(vault).List.Execute(
		ctx, vault.Path, vault.Name, vault.GoalsFolder, nil, true, "", "",
	)
}

// findGoal returns the goal row with the given id, or false when absent.
func (s *service) findGoal(
	ctx context.Context, vault vaultconfig.Vault, goalID string,
) (ops.TaskListItem, bool, error) {
	goals, err := s.listGoals(ctx, vault)
	if err != nil {
		return ops.TaskListItem{}, false, err
	}
	for _, goal := range goals {
		if goal.Name == goalID {
			return goal, true, nil
		}
	}
	return ops.TaskListItem{}, false, nil
}
