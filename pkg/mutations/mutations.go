// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package mutations implements the write side of the vault-ui HTTP API: the
// task and goal session lifecycle, frontmatter writes, command execution, and
// the cache/config reload routes. It reproduces the Python
// vault_ui.api.tasks write handlers — the same status codes, bodies, guards,
// and vault-file side effects — so the frozen frontend behaves identically.
//
// The nine frontmatter-writing routes (task phase/status/flag/assign-to-me/
// session set/session clear, goal status/assign-to-me/session clear) answer 202
// instead of 200 (spec 025): they validate synchronously, enqueue one write on
// the vault's queue, and answer with today's typed body carrying the requested
// value. Every other status code, body and guard still mirrors the Python
// handlers.
//
// All vault access goes through vault-cli's exported Go API (a library call);
// no vault-cli subprocess is ever spawned.
package mutations

import (
	"context"
	"strings"

	"github.com/bborbe/errors"
	"github.com/bborbe/run"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/ops"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/pageindex"
	"github.com/bborbe/vault-ui/pkg/queue"
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
// refresh to connected clients, and announces a failed queued write so the
// board can revert. websocket.MutationPublisher satisfies it structurally.
//
//counterfeiter:generate -o ./mocks/event_publisher.go --fake-name EventPublisher . EventPublisher
type EventPublisher interface {
	PublishTaskUpdated(ctx context.Context, vault, taskID string)
	PublishGoalUpdated(ctx context.Context, vault, goalID string)
	PublishWriteFailed(ctx context.Context, vault, itemKind, itemID, reason string)
}

// WriteQueue accepts one vault write for asynchronous, per-vault FIFO
// application. queue.Queue satisfies it.
//
//counterfeiter:generate -o ./mocks/write_queue.go --fake-name WriteQueue . WriteQueue
type WriteQueue interface {
	Enqueue(ctx context.Context, write queue.Write) error
}

// IndexInvalidator marks the read-side page index stale after a vault-ui write,
// so the next list read re-reads what the write changed. MarkDirty marks whole
// folders, whose next read compares fingerprints and re-reads only what
// changed; ForceReload is the operator escape hatch behind the cache-reload
// route. It deliberately exposes no page-content read method: mutations keep
// reading vault files directly.
//
//counterfeiter:generate -o ./mocks/index_invalidator.go --fake-name IndexInvalidator . IndexInvalidator
type IndexInvalidator interface {
	MarkDirty(keys ...pageindex.Key)
	ForceReload()
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
	Index        IndexInvalidator
	Queue        WriteQueue
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

// markVaultDirty marks the vault's tasks and goals keys dirty in the page
// index. The keys are derived exactly as the read side derives them.
func (s *service) markVaultDirty(vault vaultconfig.Vault) {
	s.deps.Index.MarkDirty(
		pageindex.NewKey(vault.Path, vault.TasksFolder),
		pageindex.NewKey(vault.Path, vault.GoalsFolder),
	)
}

// enqueueWrite queues one frontmatter write on the vault's FIFO. write runs
// the vault-cli calls; onSuccess runs the route's post-write side-effects.
// Both run on the vault's queue consumer with the consumer's context — never
// on the request goroutine and never with the request context. A failed (or
// panicking) write publishes a write_failed frame and runs none of onSuccess.
//
// A write that fails after a partial file change is picked up by the file
// watcher, which refreshes the page index on its own; the queue never retries.
func (s *service) enqueueWrite(
	ctx context.Context,
	vault vaultconfig.Vault,
	itemKind, itemID string,
	write run.Func,
	onSuccess func(ctx context.Context),
) error {
	if err := s.deps.Queue.Enqueue(ctx, queue.Write{
		Vault:  vault.Name,
		ItemID: itemID,
		Apply: func(ctx context.Context) error {
			if err := run.CatchPanic(write)(ctx); err != nil {
				s.deps.Publisher.PublishWriteFailed(
					ctx, vault.Name, itemKind, itemID, failureReason(err),
				)
				return errors.Wrapf(
					ctx, err, "apply %s write %s in vault %s", itemKind, itemID, vault.Name,
				)
			}
			onSuccess(ctx)
			return nil
		},
	}); err != nil {
		return errors.Wrapf(ctx, err, "enqueue %s write %s in vault %s", itemKind, itemID, vault.Name)
	}
	return nil
}

// failureReason is the human-readable reason a write_failed frame carries: an
// HTTPError's Detail (the text today's synchronous route would have answered),
// otherwise the error text.
func failureReason(err error) string {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Detail
	}
	return err.Error()
}

// taskWritten is the post-write side-effect of a publishing task route, in
// today's order: status cache, page-index dirty mark, then the frame.
func (s *service) taskWritten(vault vaultconfig.Vault, taskID string) func(context.Context) {
	return func(ctx context.Context) {
		s.deps.Cache.Invalidate(vault.Name, taskID)
		s.markVaultDirty(vault)
		s.deps.Publisher.PublishTaskUpdated(ctx, vault.Name, taskID)
	}
}

// goalWritten is taskWritten for a publishing goal route.
func (s *service) goalWritten(vault vaultconfig.Vault, goalID string) func(context.Context) {
	return func(ctx context.Context) {
		s.deps.Cache.Invalidate(vault.Name, goalID)
		s.markVaultDirty(vault)
		s.deps.Publisher.PublishGoalUpdated(ctx, vault.Name, goalID)
	}
}

// itemWrittenSilently is the post-write side-effect of the task session
// routes, which publish no frame today: status cache, then dirty mark.
func (s *service) itemWrittenSilently(
	vault vaultconfig.Vault, itemID string,
) func(context.Context) {
	return func(context.Context) {
		s.deps.Cache.Invalidate(vault.Name, itemID)
		s.markVaultDirty(vault)
	}
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
