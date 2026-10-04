// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build parity_selftest_body_diverge

package handler

import (
	"net/http"

	"github.com/bborbe/vault-ui/pkg/api"
)

// selftestDivergeVaultsResponse renames the `name` field in the parity harness
// self-test build, so the harness must detect a non-empty body diff and fail
// loudly.
func selftestDivergeVaultsResponse(resp http.ResponseWriter, items []api.VaultResponse) bool {
	renamed := make([]map[string]any, 0, len(items))
	for _, item := range items {
		renamed = append(renamed, map[string]any{
			"name_x":        item.Name,
			"vault_path":    item.VaultPath,
			"tasks_folder":  item.TasksFolder,
			"claude_script": item.ClaudeScript,
		})
	}
	writeJSON(resp, http.StatusOK, renamed)
	return true
}
