// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cleanup

import (
	"path/filepath"
	"strings"
)

// DeriveClaudeProjectDir returns the Claude project dir for a vault:
// homeDir/.claude/projects/<encoded>, where <encoded> is sessionProjectDir (or
// vaultPath when empty), tilde-expanded, with "/" replaced by "-".
//
// Claude stores session .jsonl files under ~/.claude/projects/<encoded-cwd>/,
// where <encoded-cwd> is the session's working directory with "/" replaced by
// "-". The transcript's location is the only per-vault evidence available: a
// session launched for this vault writes its <uuid>.jsonl under this vault's
// project dir.
func DeriveClaudeProjectDir(homeDir, vaultPath, sessionProjectDir string) string {
	source := sessionProjectDir
	if source == "" {
		source = vaultPath
	}
	expanded := expandUser(source, homeDir)
	encoded := strings.ReplaceAll(expanded, "/", "-")
	return filepath.Join(homeDir, ".claude", "projects", encoded)
}

// expandUser expands a leading "~" to homeDir, matching Python's
// Path.expanduser() for the home-directory case.
func expandUser(path, homeDir string) string {
	if path == "~" {
		return homeDir
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir, path[2:])
	}
	return path
}
