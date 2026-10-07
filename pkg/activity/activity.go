// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package activity reproduces the Python vault_ui.activity activity-date,
// transcript-mtime, and session-registry helpers as a behavior-preserving port.
package activity

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	libtime "github.com/bborbe/time"
	"github.com/golang/glog"
)

// TranscriptMtime returns the mtime of the session transcript, checking
// projectDir first and then every project directory under projectsRoot.
// It returns nil for a missing/blank session id and for a transcript that
// cannot be found or read (an expected absence, never an error).
//
// Deliberately uncached, exactly as the Python docstring says: the caller reads
// it once per request and a cache would keep a dead session looking fresh.
func TranscriptMtime(ctx context.Context, sessionID, projectDir, projectsRoot string) *libtime.DateTime {
	if sessionID == "" {
		return nil
	}

	filename := sessionID + ".jsonl"

	if direct := mtimeOrNone(filepath.Join(projectDir, filename)); direct != nil {
		return direct
	}

	root := projectsRoot
	if root == "" {
		root = DefaultProjectsRoot()
	}

	matches, err := filepath.Glob(filepath.Join(root, "*", filename))
	if err != nil {
		glog.V(4).Infof("[Activity] Cannot scan %s: %v", root, err)
		return nil
	}
	for _, path := range matches {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if found := mtimeOrNone(path); found != nil {
			return found
		}
	}

	return nil
}

// TranscriptMtimeGetter returns a session transcript's mtime, or nil when the
// transcript is absent. activity.TranscriptMtime is the default implementation.
type TranscriptMtimeGetter func(
	ctx context.Context,
	sessionID, projectDir, projectsRoot string,
) *libtime.DateTime

// ComputeActivityDate returns the newer of the task-file mtime and the transcript
// mtime; nil only when both signals are absent. A nil modifiedDate is an absent
// task-file mtime.
func ComputeActivityDate(
	ctx context.Context,
	modifiedDate *libtime.DateTime,
	sessionID, projectDir, projectsRoot string,
) *libtime.DateTime {
	return ComputeActivityDateWith(
		ctx, TranscriptMtime, modifiedDate, sessionID, projectDir, projectsRoot,
	)
}

// ComputeActivityDateWith is ComputeActivityDate with an injected transcript
// probe, so a caller can supply a cached probe instead of touching the
// filesystem. A nil probe means TranscriptMtime.
func ComputeActivityDateWith(
	ctx context.Context,
	probe TranscriptMtimeGetter,
	modifiedDate *libtime.DateTime,
	sessionID, projectDir, projectsRoot string,
) *libtime.DateTime {
	candidates := make([]libtime.DateTime, 0, 2)
	if modifiedDate != nil {
		candidates = append(candidates, modifiedDate.UTC())
	}
	if probe == nil {
		probe = TranscriptMtime
	}
	if transcript := probe(ctx, sessionID, projectDir, projectsRoot); transcript != nil {
		candidates = append(candidates, *transcript)
	}
	if len(candidates) == 0 {
		return nil
	}

	newest := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.After(newest) {
			newest = candidate
		}
	}
	return newest.Ptr()
}

// ReadRegistrySessionIDs returns the sessionId of every entry in the harness
// session registry directory. Never raises: a missing/unreadable directory, an
// unreadable file, a non-JSON file, and a file with no sessionId each contribute
// nothing.
//
// Deliberately uncached — the caller reads it once per request, and a cache
// would keep a registry-live session rendering live after its process exits.
func ReadRegistrySessionIDs(ctx context.Context, root string) []string {
	ids := []string{}

	entries, err := os.ReadDir(root)
	if err != nil {
		glog.V(4).Infof("[Activity] Cannot read session registry %s: %v", root, err)
		return ids
	}

	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return ids
		default:
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			glog.V(4).Infof("[Activity] Cannot read session registry entry %s: %v", path, readErr)
			continue
		}
		var payload map[string]any
		if unmarshalErr := json.Unmarshal(data, &payload); unmarshalErr != nil {
			glog.V(4).Infof(
				"[Activity] Cannot read session registry entry %s: %v",
				path,
				unmarshalErr,
			)
			continue
		}
		if sessionID, ok := payload["sessionId"].(string); ok && sessionID != "" {
			ids = append(ids, sessionID)
		}
	}

	return ids
}

// DefaultRegistryRoot returns the harness registry root (`~/.claude/sessions`).
func DefaultRegistryRoot() string {
	return filepath.Join(homeDir(), ".claude", "sessions")
}

// DefaultProjectsRoot returns the transcript projects root (`~/.claude/projects`).
func DefaultProjectsRoot() string {
	return filepath.Join(homeDir(), ".claude", "projects")
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func mtimeOrNone(path string) *libtime.DateTime {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	return libtime.DateTime(info.ModTime()).UTC().Ptr()
}
