// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mutations

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// shellQuote reproduces Python's shlex.quote for the strings the resume command
// carries. A value made only of shell-safe characters is returned unchanged;
// anything else is single-quoted with embedded quotes escaped.
func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	safe := true
	for _, r := range value {
		if !isShellSafe(r) {
			safe = false
			break
		}
	}
	if safe {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// isShellSafe reports whether r belongs to shlex's "safe" set.
func isShellSafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	switch r {
	case '_', '-', '.', '/', ':', '@', '%', '+', ',', '=', '~':
		return true
	default:
		return false
	}
}

// expandTilde expands a leading "~" to the current home directory, matching
// Python's Path.expanduser() for the home case.
func expandTilde(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

// sessionStartedMarker is the ISO-8601 UTC instant written to
// claude_session_started when a launch begins.
func sessionStartedMarker() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
