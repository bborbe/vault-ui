// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"
	"slices"

	"github.com/bborbe/errors"
	"github.com/bborbe/vault-cli/pkg/ops"
)

// NewListOperation returns an ops.ListOperation backed by the source's cache.
// The board's call sites pass the folder they want, and one operation serves
// tasks, goals and topics alike: the folder selects the cached list, and each
// item is mapped onto vault-cli's TaskListItem so the board's row derivation is
// unchanged.
func NewListOperation(
	source *Source,
	vaultName string,
	tasksFolder string,
	goalsFolder string,
	topicsFolder string,
) ops.ListOperation {
	return &listOperation{
		source:       source,
		vaultName:    vaultName,
		tasksFolder:  tasksFolder,
		goalsFolder:  goalsFolder,
		topicsFolder: topicsFolder,
	}
}

type listOperation struct {
	source       *Source
	vaultName    string
	tasksFolder  string
	goalsFolder  string
	topicsFolder string
}

// Execute implements ops.ListOperation. The board passes showAll=true and no
// filters, so every cached item for the vault is returned and the board applies
// its own visibility rules; a caller that does filter is honoured here too.
// pagesDir selects the kind — reading every kind from the task list is the bug
// this switch exists to prevent.
func (l *listOperation) Execute(
	ctx context.Context,
	_ string,
	vaultName string,
	pagesDir string,
	statusFilters []string,
	_ bool,
	assigneeFilter string,
	goalFilter string,
) ([]ops.TaskListItem, error) {
	name := l.vaultName
	if name == "" {
		name = vaultName
	}
	switch {
	case l.topicsFolder != "" && pagesDir == l.topicsFolder:
		return l.topics(name, statusFilters, assigneeFilter), nil
	case l.goalsFolder != "" && pagesDir == l.goalsFolder:
		return l.goals(name, statusFilters, assigneeFilter), nil
	case l.tasksFolder == "" || pagesDir == l.tasksFolder:
		return l.tasks(name, statusFilters, assigneeFilter, goalFilter), nil
	default:
		// An unknown folder must not fall through to the task list: serving one
		// kind's rows under another kind's view is the defect this switch fixes.
		return nil, errors.Errorf(ctx, "list %q: not a task, goal or topic folder", pagesDir)
	}
}

// tasks maps the vault's cached tasks onto list items.
func (l *listOperation) tasks(
	name string,
	statusFilters []string,
	assigneeFilter string,
	goalFilter string,
) []ops.TaskListItem {
	items := make([]ops.TaskListItem, 0)
	for _, t := range l.source.Tasks(name) {
		if !matchesFilters(t.Status, t.Assignee, t.Goals, statusFilters, assigneeFilter, goalFilter) {
			continue
		}
		items = append(items, ops.TaskListItem{
			Name:            t.Title,
			Status:          t.Status,
			Assignee:        t.Assignee,
			Priority:        t.Priority,
			Vault:           t.Vault,
			DeferDate:       deref(t.DeferDate),
			PlannedDate:     deref(t.PlannedDate),
			DueDate:         deref(t.DueDate),
			ClaudeSessionID: deref(t.ClaudeSessionID),
			Phase:           t.Phase,
			ModifiedDate:    t.UpdatedAt,
			Goals:           t.Goals,
			Flag:            t.Flag,
			BlockedBy:       t.BlockedBy,
		})
	}
	return items
}

// goals maps the vault's cached goals onto list items. A goal carries its
// assignee and priority like a task, so the assignee filter and the card's
// assignee/priority render; its phase is carried so the board can group goals
// into phase columns.
func (l *listOperation) goals(
	name string,
	statusFilters []string,
	assigneeFilter string,
) []ops.TaskListItem {
	items := make([]ops.TaskListItem, 0)
	for _, g := range l.source.Goals(name) {
		if !matchesFilters(g.Status, g.Assignee, nil, statusFilters, assigneeFilter, "") {
			continue
		}
		items = append(items, ops.TaskListItem{
			Name:            g.Title,
			Status:          g.Status,
			Phase:           g.Phase,
			Assignee:        g.Assignee,
			Priority:        g.Priority,
			Vault:           g.Vault,
			ClaudeSessionID: deref(g.ClaudeSessionID),
			Flag:            g.Flag,
		})
	}
	return items
}

// topics maps the vault's cached topics onto list items. A topic carries no
// priority in vault-api's projection today, so it stays zero unless the API adds
// one; its Goals list is what the topic detail view
// resolves against, and its phase is carried for the phase columns.
func (l *listOperation) topics(
	name string,
	statusFilters []string,
	assigneeFilter string,
) []ops.TaskListItem {
	items := make([]ops.TaskListItem, 0)
	for _, t := range l.source.Topics(name) {
		if !matchesFilters(t.Status, t.Assignee, nil, statusFilters, assigneeFilter, "") {
			continue
		}
		items = append(items, ops.TaskListItem{
			Name:            t.Title,
			Status:          t.Status,
			Phase:           t.Phase,
			Assignee:        t.Assignee,
			Priority:        t.Priority,
			Vault:           t.Vault,
			ClaudeSessionID: deref(t.ClaudeSessionID),
			Goals:           t.Goals,
			Flag:            t.Flag,
		})
	}
	return items
}

// matchesFilters applies the optional list filters.
func matchesFilters(
	status string,
	assignee string,
	goals []string,
	statusFilters []string,
	assigneeFilter string,
	goalFilter string,
) bool {
	if len(statusFilters) > 0 && !slices.Contains(statusFilters, status) {
		return false
	}
	if assigneeFilter != "" && assignee != assigneeFilter {
		return false
	}
	if goalFilter != "" && !slices.Contains(goals, goalFilter) {
		return false
	}
	return true
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
