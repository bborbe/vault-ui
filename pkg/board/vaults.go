// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board

import (
	"context"
	"slices"
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
	var indexed []string
	if b.source != nil {
		indexed = b.source.IndexedVaults()
	}
	// indexedSet stays nil when the coverage set is unknown, so a nil set is
	// never mistaken for "serves nothing".
	var indexedSet map[string]bool
	if indexed != nil {
		indexedSet = make(map[string]bool, len(indexed))
		for _, name := range indexed {
			indexedSet[strings.ToLower(name)] = true
		}
	}

	responses := make([]api.VaultResponse, 0, len(vaults))
	for _, vault := range vaults {
		var source *bool
		if indexedSet != nil {
			served := indexedSet[strings.ToLower(vault.Name)]
			source = &served
		}
		responses = append(responses, api.VaultResponse{
			Name:         vault.Name,
			VaultPath:    vault.Path,
			TasksFolder:  vault.TasksFolder,
			ClaudeScript: vault.ClaudeScript,
			Source:       source,
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

	named := make([]assigneeSortKey, 0, len(namedSet))
	for name := range namedSet {
		named = append(named, assigneeSortKey{name: name, lower: strings.ToLower(name)})
	}
	slices.SortFunc(named, compareAssigneeKeys)

	ordered := make([]string, 0, len(named))
	for _, entry := range named {
		ordered = append(ordered, entry.name)
	}

	return api.AssigneesResponse{Named: ordered, HasUnassigned: hasUnassigned}, nil
}

// assigneeSortKey is one assignee with its lowercase form precomputed, so the
// sort's comparator never allocates: strings.ToLower allocates for any name with
// an uppercase byte, and computing it per comparison allocated O(n log n) times
// per read.
type assigneeSortKey struct {
	name  string
	lower string
}

// compareAssigneeKeys orders by the lowercase form, then by the original name as
// a tiebreak. It allocates nothing, so slices.SortFunc over it reports 0
// allocs/op.
func compareAssigneeKeys(left, right assigneeSortKey) int {
	if left.lower != right.lower {
		return strings.Compare(left.lower, right.lower)
	}
	return strings.Compare(left.name, right.name)
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
