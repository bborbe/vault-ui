// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board

import (
	"context"
	"sort"
	"strings"

	"github.com/bborbe/errors"

	"github.com/bborbe/vault-ui/pkg/api"
)

// ListVaults reproduces GET /api/vaults.
func (b *board) ListVaults(ctx context.Context) ([]api.VaultResponse, error) {
	vaults, err := b.vaults.Vaults(ctx)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "list vaults")
	}
	responses := make([]api.VaultResponse, 0, len(vaults))
	for _, vault := range vaults {
		responses = append(responses, api.VaultResponse{
			Name:         vault.Name,
			VaultPath:    vault.Path,
			TasksFolder:  vault.TasksFolder,
			ClaudeScript: vault.ClaudeScript,
		})
	}
	return responses, nil
}

// ListAssignees reproduces GET /api/assignees.
func (b *board) ListAssignees(ctx context.Context, vaults []string) (api.AssigneesResponse, error) {
	all, err := b.vaults.Vaults(ctx)
	if err != nil {
		return api.AssigneesResponse{}, errors.Wrap(ctx, err, "list vaults")
	}
	selected := b.selectVaults(all, vaults)

	namedSet := map[string]bool{}
	hasUnassigned := false
	for _, vault := range selected {
		rows, snapshotErr := b.snapshot.List(ctx, vault)
		if snapshotErr != nil {
			return api.AssigneesResponse{}, snapshotErr
		}
		for _, row := range rows {
			if strings.TrimSpace(row.item.Assignee) != "" {
				namedSet[row.item.Assignee] = true
			} else {
				hasUnassigned = true
			}
		}
	}

	named := make([]string, 0, len(namedSet))
	for name := range namedSet {
		named = append(named, name)
	}
	sort.Slice(named, func(i, j int) bool {
		left := strings.ToLower(named[i])
		right := strings.ToLower(named[j])
		if left != right {
			return left < right
		}
		return named[i] < named[j]
	})

	return api.AssigneesResponse{Named: named, HasUnassigned: hasUnassigned}, nil
}

// selectVaults resolves the requested vault names against the configured set,
// preserving the requested order and skipping unknown names (the Python
// ValueError → continue degrade path). A nil/empty filter selects every vault
// in config order.
func (b *board) selectVaults(all []Vault, raw []string) []Vault {
	filter := flattenFilter(raw)
	if filter == nil {
		return all
	}
	byName := make(map[string]Vault, len(all))
	for _, vault := range all {
		byName[vault.Name] = vault
	}
	selected := make([]Vault, 0, len(filter))
	for _, name := range filter {
		if vault, ok := byName[name]; ok {
			selected = append(selected, vault)
		}
	}
	return selected
}

// findVault resolves a single vault by name.
func (b *board) findVault(all []Vault, name string) (Vault, bool) {
	for _, vault := range all {
		if vault.Name == name {
			return vault, true
		}
	}
	return Vault{}, false
}
