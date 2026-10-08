// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/bborbe/errors"
	"github.com/bborbe/vault-cli/pkg/ops"

	"github.com/bborbe/vault-ui/pkg/activity"
	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/cleanup"
	"github.com/bborbe/vault-ui/pkg/hierarchy"
	"github.com/bborbe/vault-ui/pkg/session"
)

// fallbackGoalsFolder mirrors the Python "23 Goals" fallback.
const fallbackGoalsFolder = "23 Goals"

// ListGoals reproduces GET /api/goals.
func (b *board) ListGoals(ctx context.Context, query GoalQuery) ([]api.GoalResponse, error) {
	all, err := b.vaults.Vaults(ctx)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "list vaults")
	}
	selected := b.selectVaults(all, query.Vaults)

	statusFilter := flattenFilter(query.Statuses)
	assigneeFilter := flattenAssigneeFilter(query.Assignees)

	now := b.clock.Now().UTC().Time()
	cutoff := now.Add(time.Duration(query.UpcomingHours) * time.Hour)

	registryIDs := b.signals.RegistrySessionIDs(ctx)
	liveIDsUnknown := !b.signals.LiveIDsKnown()
	resumeIDs := b.signals.ResumeSessionIDs(ctx)

	responses := make([]api.GoalResponse, 0, len(selected))
	for _, vault := range selected {
		rows, rowErr := b.goalsForVault(
			ctx, vault, statusFilter, assigneeFilter, now, cutoff,
			registryIDs, resumeIDs, liveIDsUnknown,
		)
		if rowErr != nil {
			return nil, rowErr
		}
		responses = append(responses, rows...)
	}
	return responses, nil
}

type goalRow struct {
	item     ops.TaskListItem
	upcoming bool
}

func (b *board) goalsForVault(
	ctx context.Context,
	vault Vault,
	statusFilter, assigneeFilter []string,
	now, cutoff time.Time,
	registryIDs, resumeIDs []string,
	liveIDsUnknown bool,
) ([]api.GoalResponse, error) {
	items, err := b.ops.List(vault).Execute(
		ctx, vault.Path, vault.Name, vault.GoalsFolder, nil, true, "", "",
	)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "list goals for vault %s", vault.Name)
	}

	rows := make([]goalRow, 0, len(items))
	for _, item := range items {
		if statusFilter != nil && !hasString(statusFilter, item.Status) {
			continue
		}
		if !assigneeMatches(item.Assignee, assigneeFilter) {
			continue
		}
		row := goalRow{item: item}
		if item.Status == "completed" || item.DeferDate == "" {
			rows = append(rows, row)
			continue
		}
		deferAt, ok := parseDeferDate(item.DeferDate)
		if !ok {
			return nil, errors.Errorf(ctx, "invalid defer_date %q", item.DeferDate)
		}
		switch {
		case !deferAt.After(now):
			rows = append(rows, row)
		case !deferAt.After(cutoff):
			row.upcoming = true
			rows = append(rows, row)
		}
	}

	goalsFolder := goalsFolderName(vault.Path)
	projectDir := cleanup.DeriveClaudeProjectDir(b.homeDir, vault.Path, vault.SessionProjectDir)
	projectsRoot := filepath.Join(b.homeDir, ".claude", "projects")

	responses := make([]api.GoalResponse, 0, len(rows))
	for _, row := range rows {
		responses = append(responses, b.goalResponse(
			ctx, vault, row, goalsFolder, projectDir, projectsRoot,
			registryIDs, resumeIDs, liveIDsUnknown,
		))
	}
	return responses, nil
}

func (b *board) goalResponse(
	ctx context.Context,
	vault Vault,
	row goalRow,
	goalsFolder, projectDir, projectsRoot string,
	registryIDs, resumeIDs []string,
	liveIDsUnknown bool,
) api.GoalResponse {
	item := row.item

	blockers := b.uncompletedBlockers(vault.Name, item.BlockedBy)
	if blockers == nil {
		blockers = []string{}
	}

	state := session.ClassifySessionState(ctx, session.ClassifyParams{
		SessionID:          item.ClaudeSessionID,
		ProjectDir:         projectDir,
		ProjectsRoot:       projectsRoot,
		Now:                b.clock.Now().UTC(),
		LiveWindow:         session.DefaultLiveWindow,
		ResumeSessionIDs:   resumeIDs,
		RegistrySessionIDs: registryIDs,
		LiveIDsUnknown:     liveIDsUnknown,
		TranscriptMtime:    b.transcriptProbe(),
	})

	return api.GoalResponse{
		ID:                   item.Name,
		Title:                item.Name,
		Status:               &item.Status,
		Priority:             priorityValue(item.Priority),
		ObsidianURL:          obsidianURL(vault.VaultName, goalsFolder+"/"+item.Name+".md"),
		DeferDate:            strPtr(item.DeferDate),
		TargetDate:           nil,
		CompletedDate:        strPtr(item.CompletedDate),
		Vault:                vault.Name,
		ClaudeSessionID:      strPtr(item.ClaudeSessionID),
		ClaudeSessionStarted: b.sessionStarted(vault.Name, item.Name),
		Assignee:             strPtr(item.Assignee),
		BlockedBy:            item.BlockedBy,
		Blocked:              len(blockers) > 0,
		Blockers:             blockers,
		Upcoming:             row.upcoming,
		ActivityDate: dateTimeString(activity.ComputeActivityDateWith(
			ctx,
			b.transcriptProbe(),
			parseDateTime(item.ModifiedDate),
			item.ClaudeSessionID,
			projectDir,
			projectsRoot,
		)),
		SessionState: sessionStatePtr(state),
	}
}

// goalsFolderName returns the first discovered *Goals folder name, or the
// Python fallback. Only the folder's base name is used.
func goalsFolderName(vaultPath string) string {
	folders, err := hierarchy.DiscoverHierarchyFolders(vaultPath)
	if err != nil {
		return fallbackGoalsFolder
	}
	for _, folder := range folders {
		name := filepath.Base(folder)
		if strings.HasSuffix(name, "Goals") {
			return name
		}
	}
	return fallbackGoalsFolder
}
