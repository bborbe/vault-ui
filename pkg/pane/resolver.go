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
	// ResolveAll resolves every id in sessionIDs in one batch: it reads the
	// registry once and runs `wezterm cli list` once for the whole batch. The
	// result is keyed by the caller's own session id — not the registry's, which
	// may be longer — and an id that resolves to nothing is simply absent. An
	// empty input, a failed listing and a cancelled context all yield an empty
	// non-nil map; the signature carries no error on purpose.
	ResolveAll(ctx context.Context, sessionIDs []string) map[string]string
}

// weztermPane is one entry of `wezterm cli list --format json`.
type weztermPane struct {
	PaneID int    `json:"pane_id"`
	Title  string `json:"title"`
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

	panes, err := r.listPanes(ctx)
	if err != nil {
		logDebug("[PaneResolver] wezterm cli list failed for session %s: %v", sessionID, err)
		return "", false
	}

	paneID, ok := matchPaneID(panes, name)
	if !ok {
		logDebug("[PaneResolver] no unambiguous pane titled %q for session %s", name, sessionID)
		return "", false
	}
	return paneID, true
}

// ResolveAll resolves every id in sessionIDs in one batch. It reads the
// registry once and runs `wezterm cli list` once, and only when at least one id
// has a registry name. The result is keyed by the caller's own id; an id that
// resolves to nothing is absent, never present with an empty value. Every
// failure yields an empty non-nil map.
func (r *resolver) ResolveAll(ctx context.Context, sessionIDs []string) map[string]string {
	resolved := map[string]string{}
	if len(sessionIDs) == 0 {
		return resolved
	}

	registry := r.registryNames(ctx, r.registryDir)
	type candidate struct {
		sessionID string
		name      string
	}
	candidates := make([]candidate, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		if sessionID == "" {
			continue
		}
		name, ok := candidateNameFrom(registry, sessionID)
		if !ok {
			continue
		}
		candidates = append(candidates, candidate{sessionID: sessionID, name: name})
	}
	if len(candidates) == 0 {
		return resolved
	}

	panes, err := r.listPanes(ctx)
	if err != nil {
		logDebug("[PaneResolver] wezterm cli list failed for %d session(s): %v", len(candidates), err)
		return resolved
	}

	for _, entry := range candidates {
		if paneID, ok := matchPaneID(panes, entry.name); ok {
			resolved[entry.sessionID] = paneID
		}
	}
	return resolved
}

// candidateName returns the stripped name of the single registry entry whose
// session id has sessionID as a case-insensitive prefix, or ("", false) when
// there is no such entry, more than one, or the name is empty.
func (r *resolver) candidateName(ctx context.Context, sessionID string) (string, bool) {
	return candidateNameFrom(r.registryNames(ctx, r.registryDir), sessionID)
}

// candidateNameFrom applies the registry-name rule to an already-read registry:
// exactly one entry whose session id has sessionID as a case-insensitive prefix,
// with its status glyph stripped, and a non-empty result. It is shared by the
// single and batch paths so the two cannot drift.
func candidateNameFrom(registry map[string]string, sessionID string) (string, bool) {
	prefix := strings.ToLower(sessionID)
	names := make([]string, 0, 1)
	for id, entryName := range registry {
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

// matchPaneID applies the pane-title rule: exactly one pane whose
// glyph-stripped title equals name resolves; zero or several resolve nothing.
// It is shared by the single and batch paths so the two cannot drift.
func matchPaneID(panes []weztermPane, name string) (string, bool) {
	matches := make([]int, 0, 1)
	for _, weztermPane := range panes {
		if stripStatusGlyph(weztermPane.Title) == name {
			matches = append(matches, weztermPane.PaneID)
		}
	}
	if len(matches) != 1 {
		return "", false
	}
	return strconv.Itoa(matches[0]), true
}

// listPanes runs `wezterm cli list --format json` once and decodes its output.
// ctx is threaded to r.exec exactly as the click path does, so the configured
// timeout still bounds the call.
func (r *resolver) listPanes(ctx context.Context) ([]weztermPane, error) {
	env := BuildSubprocessEnv(os.Environ(), r.homeDir, r.bundleDir, PidAlive)
	output, err := r.exec(ctx, env, weztermBinary(r.bundleDir), "cli", "list", "--format", "json")
	if err != nil {
		return nil, err
	}
	var panes []weztermPane
	if unmarshalErr := json.Unmarshal(output, &panes); unmarshalErr != nil {
		return nil, errors.Wrap(ctx, unmarshalErr, "parse wezterm cli list output")
	}
	return panes, nil
}

// weztermBinary returns the absolute wezterm path when the app bundle holds the
// binary, else the bare name. exec resolves a bare name against this process's
// own PATH, not the child env, so prepending the bundle dir to the child PATH
// alone never finds wezterm under launchd, whose PATH lacks the bundle.
func weztermBinary(bundleDir string) string {
	if binDir, ok := WeztermBinDir(bundleDir); ok {
		return filepath.Join(binDir, "wezterm")
	}
	return "wezterm"
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
