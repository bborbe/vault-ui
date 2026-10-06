// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pane

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// ExecFunc runs one external command with env and returns its stdout.
type ExecFunc func(
	ctx context.Context,
	env []string,
	name string,
	args ...string,
) ([]byte, error)

// RegistryNamesFunc reads the harness session registry under dir and returns
// session id -> the session's current name.
type RegistryNamesFunc func(ctx context.Context, dir string) map[string]string

// ResolverParams carries the resolver's injectable dependencies. Exec and
// RegistryNames fall back to the real implementations when nil; a non-positive
// Timeout falls back to DefaultResolveTimeout; an empty RegistryDir falls back
// to <HomeDir>/.claude/sessions.
type ResolverParams struct {
	HomeDir       string
	BundleDir     string
	RegistryDir   string
	Timeout       time.Duration
	Exec          ExecFunc
	RegistryNames RegistryNamesFunc
}

// Resolver resolves a live session id to its WezTerm pane id.
type Resolver interface {
	Resolve(ctx context.Context, sessionID string) (string, bool)
}

type resolver struct {
	homeDir       string
	bundleDir     string
	registryDir   string
	exec          ExecFunc
	registryNames RegistryNamesFunc
}

// NewResolver creates a Resolver. It only fills defaults and stores them — no
// resolution work happens here.
func NewResolver(params ResolverParams) Resolver {
	timeout := params.Timeout
	if timeout <= 0 {
		timeout = DefaultResolveTimeout
	}

	registryDir := params.RegistryDir
	if registryDir == "" {
		registryDir = filepath.Join(params.HomeDir, ".claude", "sessions")
	}

	execFn := params.Exec
	if execFn == nil {
		execFn = func(
			ctx context.Context,
			env []string,
			name string,
			args ...string,
		) ([]byte, error) {
			return runCommand(ctx, timeout, env, name, args...)
		}
	}

	registryNames := params.RegistryNames
	if registryNames == nil {
		registryNames = readRegistryNames
	}

	return &resolver{
		homeDir:       params.HomeDir,
		bundleDir:     params.BundleDir,
		registryDir:   registryDir,
		exec:          execFn,
		registryNames: registryNames,
	}
}

// Resolve returns ("", false) for every failure and (paneID, true) only on
// exactly one unambiguous match: the registry entry whose session id carries the
// requested id as a case-insensitive prefix, whose name matches exactly one
// WezTerm pane title once both sides have their status glyph stripped.
func (r *resolver) Resolve(ctx context.Context, sessionID string) (string, bool) {
	if sessionID == "" {
		return "", false
	}

	name, ok := r.candidateName(ctx, sessionID)
	if !ok {
		return "", false
	}

	env := BuildSubprocessEnv(os.Environ(), r.homeDir, r.bundleDir, PidAlive)
	output, err := r.exec(ctx, env, "wezterm", "cli", "list", "--format", "json")
	if err != nil {
		logDebug("[PaneResolver] wezterm cli list failed for session %s: %v", sessionID, err)
		return "", false
	}

	var panes []struct {
		PaneID int    `json:"pane_id"`
		Title  string `json:"title"`
	}
	if unmarshalErr := json.Unmarshal(output, &panes); unmarshalErr != nil {
		logDebug(
			"[PaneResolver] cannot parse wezterm cli list output for session %s: %v",
			sessionID,
			unmarshalErr,
		)
		return "", false
	}

	matches := make([]int, 0, 1)
	for _, weztermPane := range panes {
		if stripStatusGlyph(weztermPane.Title) == name {
			matches = append(matches, weztermPane.PaneID)
		}
	}
	if len(matches) != 1 {
		logDebug(
			"[PaneResolver] %d pane(s) titled %q for session %s",
			len(matches),
			name,
			sessionID,
		)
		return "", false
	}

	return strconv.Itoa(matches[0]), true
}

// candidateName returns the stripped name of the single registry entry whose
// session id has sessionID as a case-insensitive prefix, or ("", false) when
// there is no such entry, more than one, or the name is empty.
func (r *resolver) candidateName(ctx context.Context, sessionID string) (string, bool) {
	prefix := strings.ToLower(sessionID)
	names := make([]string, 0, 1)
	for id, entryName := range r.registryNames(ctx, r.registryDir) {
		if strings.HasPrefix(strings.ToLower(id), prefix) {
			names = append(names, entryName)
		}
	}
	if len(names) != 1 {
		return "", false
	}
	name := stripStatusGlyph(names[0])
	if name == "" {
		return "", false
	}
	return name, true
}

// stripStatusGlyph trims text, then drops leading runes while the first rune is
// neither alphanumeric nor one of /~._-, stripping the whitespace after each
// dropped rune. It reproduces the supervisor's strip_status_glyph, which is why
// "✳ Fleet Manager" matches the registry name "Fleet Manager".
func stripStatusGlyph(text string) string {
	text = strings.TrimSpace(text)
	for {
		runes := []rune(text)
		if len(runes) == 0 {
			return ""
		}
		if isNameRune(runes[0]) {
			return text
		}
		text = strings.TrimSpace(string(runes[1:]))
	}
}

// isNameRune reports whether r may start a stripped session name.
func isNameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("/~._-", r)
}

// runCommand runs one external command with env, bounded by timeout, and
// returns its stdout. It never builds a shell string and never passes argv
// through a shell.
func runCommand(
	ctx context.Context,
	timeout time.Duration,
	env []string,
	name string,
	args ...string,
) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// #nosec G204 -- argv from configuration, never a shell
	cmd := exec.CommandContext(runCtx, name, args...)
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, errors.Wrapf(ctx, err, "run %s: %s", name, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// readRegistryNames returns session id -> current name for every entry in the
// harness session registry directory. Never raises: a missing/unreadable
// directory yields an empty map, and an unreadable file, a non-JSON file, and a
// file with no sessionId each contribute nothing. The `.key` files beside the
// entries are not session entries and are skipped by the `.json` filter.
func readRegistryNames(ctx context.Context, dir string) map[string]string {
	names := map[string]string{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		glog.V(4).Infof("[PaneResolver] Cannot read session registry %s: %v", dir, err)
		return names
	}

	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return names
		default:
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		// #nosec G304 -- path from configuration, never request input
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			glog.V(4).Infof("[PaneResolver] Cannot read session registry entry %s: %v", path, readErr)
			continue
		}
		var payload struct {
			SessionID string `json:"sessionId"`
			Name      string `json:"name"`
		}
		if unmarshalErr := json.Unmarshal(data, &payload); unmarshalErr != nil {
			glog.V(4).Infof(
				"[PaneResolver] Cannot parse session registry entry %s: %v",
				path,
				unmarshalErr,
			)
			continue
		}
		if payload.SessionID == "" {
			continue
		}
		names[payload.SessionID] = payload.Name
	}

	return names
}
