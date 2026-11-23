// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"
	"slices"

	"github.com/bborbe/vault-cli/pkg/ops"
)

// NewListOperation returns an ops.ListOperation backed by the source's cache.
// The board's buildTaskRows calls Execute with the vault it wants; this
// operation filters the cached all-vault list to that vault and maps each item
// to vault-cli's TaskListItem, so the board's row derivation is unchanged.
func NewListOperation(source *Source, vaultName string) ops.ListOperation {
	return &listOperation{source: source, vaultName: vaultName}
}

type listOperation struct {
	source    *Source
	vaultName string
}

// Execute implements ops.ListOperation. The board passes showAll=true and no
// filters, so every cached task for the vault is returned and the board applies
// its own visibility rules; a caller that does filter is honoured here too.
func (l *listOperation) Execute(
	_ context.Context,
	_ string,
	vaultName string,
	_ string,
	statusFilters []string,
	_ bool,
	assigneeFilter string,
	goalFilter string,
) ([]ops.TaskListItem, error) {
	name := l.vaultName
	if name == "" {
		name = vaultName
	}
	tasks := l.source.Tasks(name)
	items := make([]ops.TaskListItem, 0, len(tasks))
	for _, t := range tasks {
		if len(statusFilters) > 0 && !slices.Contains(statusFilters, t.Status) {
			continue
		}
		if assigneeFilter != "" && t.Assignee != assigneeFilter {
			continue
		}
		if goalFilter != "" && !slices.Contains(t.Goals, goalFilter) {
			continue
		}
		items = append(items, toTaskListItem(t))
	}
	return items, nil
}

// toTaskListItem maps a vault-api task item onto vault-cli's list item. Name is
// the page name (vault-api's title); ModifiedDate is the page's updated_at.
func toTaskListItem(t Task) ops.TaskListItem {
	return ops.TaskListItem{
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
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
