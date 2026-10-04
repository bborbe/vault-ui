// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board

import (
	"context"
	"path/filepath"
	"sort"
	"time"

	"github.com/bborbe/errors"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/ops"

	"github.com/bborbe/vault-ui/pkg/activity"
	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/cleanup"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/session"
)

// defaultStatuses is the effective status set when no status[] is supplied.
var defaultStatuses = []string{"todo", "next", "in_progress", "hold", "completed"}

// validPhases is the phase enum; an unrecognized phase is treated as todo.
var validPhases = []string{
	"todo", "planning", "in_progress", "execution", "ai_review", "human_review", "done",
}

// taskRow is one vault-cli list item plus its derived fields.
type taskRow struct {
	item              ops.TaskListItem
	upcoming          bool
	recentlyCompleted bool
	phaseOverride     *string
	blockers          []string
	blocked           bool
	started           *string
	sessionState      *string
}

// ListTasks reproduces GET /api/tasks.
func (b *board) ListTasks(ctx context.Context, query TaskQuery) ([]api.TaskResponse, error) {
	all, err := b.vaults.Vaults(ctx)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "list vaults")
	}
	selected := b.selectVaults(all, query.Vaults)

	statusFilter := flattenFilter(query.Statuses)
	phaseFilter := flattenFilter(query.Phases)
	assigneeFilter := flattenAssigneeFilter(query.Assignees)
	goalFilter := flattenFilter(query.Goals)

	now := b.clock.Now().UTC().Time()
	cutoff := now.Add(time.Duration(query.UpcomingHours) * time.Hour)
	lookback := now.Add(-LookbackHours * time.Hour)

	registryIDs := b.signals.RegistrySessionIDs(ctx)
	resumeIDs := b.signals.ResumeSessionIDs(ctx)

	responses := make([]api.TaskResponse, 0, len(selected))
	for _, vault := range selected {
		rows, rowErr := b.tasksForVault(
			ctx, vault, statusFilter, phaseFilter, assigneeFilter, goalFilter,
			now, cutoff, lookback, registryIDs, resumeIDs,
		)
		if rowErr != nil {
			return nil, rowErr
		}
		responses = append(responses, rows...)
	}

	if query.SessionLive {
		live := make([]api.TaskResponse, 0, len(responses))
		for _, response := range responses {
			if response.SessionState != nil && *response.SessionState == string(session.SessionStateLive) {
				live = append(live, response)
			}
		}
		responses = live
	}
	return responses, nil
}

func (b *board) tasksForVault(
	ctx context.Context,
	vault Vault,
	statusFilter, phaseFilter, assigneeFilter, goalFilter []string,
	now, cutoff, lookback time.Time,
	registryIDs, resumeIDs []string,
) ([]api.TaskResponse, error) {
	effectiveStatus := statusFilter
	if effectiveStatus == nil {
		effectiveStatus = defaultStatuses
	}

	items, err := b.ops.List(vault).Execute(
		ctx, vault.Path, vault.Name, vault.TasksFolder, nil, true, "", "",
	)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "list tasks for vault %s", vault.Name)
	}

	rows := make([]taskRow, 0, len(items))
	for _, item := range items {
		if !hasString(effectiveStatus, item.Status) {
			continue
		}
		if !phaseMatches(item.Phase, phaseFilter) {
			continue
		}
		if !assigneeMatches(item.Assignee, assigneeFilter) {
			continue
		}
		if !goalMatches(goalsValue(item.Goals), goalFilter) {
			continue
		}
		row, visible, rowErr := b.visibleRow(ctx, item, now, cutoff, lookback)
		if rowErr != nil {
			return nil, errors.Wrapf(ctx, rowErr, "resolve visibility for %s", item.Name)
		}
		if visible {
			rows = append(rows, row)
		}
	}

	for i := range rows {
		rows[i].blockers = b.uncompletedBlockers(vault.Name, rows[i].item.BlockedBy)
		rows[i].blocked = len(rows[i].blockers) > 0
		rows[i].started = b.sessionStarted(vault.Name, rows[i].item.Name)
	}

	projectDir := cleanup.DeriveClaudeProjectDir(b.homeDir, vault.Path, vault.SessionProjectDir)
	projectsRoot := filepath.Join(b.homeDir, ".claude", "projects")

	for i := range rows {
		state := session.ClassifySessionState(ctx, session.ClassifyParams{
			SessionID:          rows[i].item.ClaudeSessionID,
			ProjectDir:         projectDir,
			ProjectsRoot:       projectsRoot,
			Now:                b.clock.Now().UTC(),
			LiveWindow:         session.DefaultLiveWindow,
			ResumeSessionIDs:   resumeIDs,
			RegistrySessionIDs: registryIDs,
		})
		rows[i].sessionState = sessionStatePtr(state)
	}

	paneMap := b.resolvePanes(ctx, rows)

	responses := make([]api.TaskResponse, 0, len(rows))
	for i := range rows {
		responses = append(
			responses,
			b.taskResponse(ctx, vault, rows[i], projectDir, projectsRoot, paneMap),
		)
	}
	return responses, nil
}

// visibleRow applies the completed/deferred visibility rules, returning the row
// with upcoming/recently_completed set.
func (b *board) visibleRow(
	ctx context.Context,
	item ops.TaskListItem,
	now, cutoff, lookback time.Time,
) (taskRow, bool, error) {
	row := taskRow{item: item}
	if item.Status == "completed" {
		completedAt := parseFlexibleDate(item.CompletedDate)
		if completedAt == nil {
			if modifiedAt := parseDateTime(item.ModifiedDate); modifiedAt != nil {
				value := modifiedAt.Time()
				completedAt = &value
			}
		}
		if completedAt != nil && !completedAt.Before(lookback) {
			row.recentlyCompleted = true
			done := "done"
			row.phaseOverride = &done
			return row, true, nil
		}
		return row, false, nil
	}
	if item.DeferDate == "" {
		return row, true, nil
	}
	deferAt, ok := parseDeferDate(item.DeferDate)
	if !ok {
		return row, false, errors.Errorf(ctx, "invalid defer_date %q", item.DeferDate)
	}
	if !deferAt.After(now) {
		return row, true, nil
	}
	if !deferAt.After(cutoff) {
		row.upcoming = true
		return row, true, nil
	}
	return row, false, nil
}

func (b *board) uncompletedBlockers(vaultName string, blockedBy []string) []string {
	uncompleted := make([]string, 0, len(blockedBy))
	for _, wikilink := range blockedBy {
		name := stripBrackets(wikilink)
		status, _ := b.cache.GetStatus(vaultName, name)
		if status != "completed" {
			uncompleted = append(uncompleted, name)
		}
	}
	return uncompleted
}

func (b *board) sessionStarted(vaultName, itemID string) *string {
	if state, ok := b.launch.State(vaultName, itemID); ok && state == launchregistry.Finished {
		return nil
	}
	if started, ok := b.cache.GetSessionStarted(vaultName, itemID); ok {
		return &started
	}
	return nil
}

func (b *board) resolvePanes(ctx context.Context, rows []taskRow) map[string]string {
	ids := make([]string, 0)
	seen := map[string]bool{}
	for _, row := range rows {
		if row.sessionState != nil &&
			*row.sessionState == string(session.SessionStateLive) &&
			row.item.ClaudeSessionID != "" &&
			!seen[row.item.ClaudeSessionID] {
			seen[row.item.ClaudeSessionID] = true
			ids = append(ids, row.item.ClaudeSessionID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	resolved := map[string]string{}
	for _, id := range ids {
		if paneID, ok := b.pane.Resolve(ctx, id); ok {
			resolved[id] = paneID
		}
	}
	return resolved
}

func (b *board) taskResponse(
	ctx context.Context,
	vault Vault,
	row taskRow,
	projectDir, projectsRoot string,
	paneMap map[string]string,
) api.TaskResponse {
	item := row.item

	phase := strPtr(item.Phase)
	if row.phaseOverride != nil {
		phase = row.phaseOverride
	}

	var jumpPane *string
	if row.sessionState != nil &&
		*row.sessionState == string(session.SessionStateLive) &&
		item.ClaudeSessionID != "" {
		if paneID, ok := paneMap[item.ClaudeSessionID]; ok {
			jumpPane = &paneID
		}
	}

	blockers := row.blockers
	if blockers == nil {
		blockers = []string{}
	}

	var sessionID *string
	if item.ClaudeSessionID != "" {
		sessionID = &item.ClaudeSessionID
	}

	return api.TaskResponse{
		ID:                   item.Name,
		Title:                item.Name,
		Status:               item.Status,
		Phase:                phase,
		ProjectPath:          nil,
		Description:          nil,
		ModifiedDate:         dateTimeString(parseDateTime(item.ModifiedDate)),
		CompletedDate:        strPtr(item.CompletedDate),
		ObsidianURL:          obsidianURL(vault.VaultName, vault.TasksFolder+"/"+item.Name+".md"),
		DeferDate:            strPtr(item.DeferDate),
		PlannedDate:          strPtr(item.PlannedDate),
		DueDate:              strPtr(item.DueDate),
		Priority:             priorityValue(item.Priority),
		Category:             strPtr(item.Category),
		Recurring:            strPtr(item.Recurring),
		ClaudeSessionID:      sessionID,
		ClaudeSessionStarted: row.started,
		Assignee:             strPtr(item.Assignee),
		BlockedBy:            item.BlockedBy,
		Blocked:              row.blocked,
		Blockers:             blockers,
		Upcoming:             row.upcoming,
		RecentlyCompleted:    row.recentlyCompleted,
		Vault:                vault.Name,
		Goals:                goalsValue(item.Goals),
		Flag:                 item.Flag,
		ActivityDate: dateTimeString(activity.ComputeActivityDate(
			ctx,
			parseDateTime(item.ModifiedDate),
			item.ClaudeSessionID,
			projectDir,
			projectsRoot,
		)),
		SessionState: row.sessionState,
		JumpPane:     jumpPane,
	}
}

// phaseMatches reproduces the Python phase filter: a task at a valid phase must
// be in the filter; a task at an unrecognized phase matches when "todo" is in
// the filter.
func phaseMatches(phase string, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	if hasString(validPhases, phase) {
		return hasString(filter, phase)
	}
	return hasString(filter, "todo")
}

// assigneeMatches reproduces the Python assignee filter: an empty token matches
// unassigned work, a non-empty token matches exactly.
func assigneeMatches(assignee string, filter []string) bool {
	if filter == nil {
		return true
	}
	for _, token := range filter {
		if token == "" && assignee == "" {
			return true
		}
		if token != "" && assignee == token {
			return true
		}
	}
	return false
}

// goalMatches reproduces the Python goal filter.
func goalMatches(goals []string, filter []string) bool {
	if filter == nil {
		return true
	}
	for _, goal := range goals {
		if hasString(filter, goal) {
			return true
		}
	}
	return false
}

// goalsValue strips wikilink brackets from goal names, returning nil when the
// list is empty (Python emits null).
func goalsValue(goals []string) []string {
	if len(goals) == 0 {
		return nil
	}
	stripped := make([]string, 0, len(goals))
	for _, goal := range goals {
		stripped = append(stripped, stripWikilink(goal))
	}
	if len(stripped) == 0 {
		return nil
	}
	return stripped
}

// priorityValue reproduces the Python priority read: 0 is absent (null), any
// non-zero value is a number.
func priorityValue(priority int) any {
	if priority == 0 {
		return nil
	}
	return priority
}

// dateTimeString renders a nullable libtime.DateTime as a JSON string.
func dateTimeString(value *libtime.DateTime) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Time().Format(time.RFC3339Nano)
	return &formatted
}

// parseFlexibleDate parses a date-only or RFC3339 value as UTC midnight/instant.
func parseFlexibleDate(value string) *time.Time {
	if value == "" {
		return nil
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		utc := parsed.UTC()
		return &utc
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		utc := parsed.UTC()
		return &utc
	}
	return nil
}

// sessionStatePtr renders the classification as a nullable string: the "none"
// state (empty session id) is Python None.
func sessionStatePtr(state session.SessionState) *string {
	if state == session.SessionStateNone {
		return nil
	}
	value := string(state)
	return &value
}
