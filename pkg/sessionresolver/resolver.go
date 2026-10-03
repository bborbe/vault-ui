// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package sessionresolver reproduces the Python vault_ui.session_resolver
// display-name to UUID resolution as a behavior-preserving port.
package sessionresolver

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/golang/glog"
)

// maxLineBytes is the Python _MAX_LINE_BYTES: a raw transcript line longer than
// this is skipped rather than parsed.
const maxLineBytes = 4096

var uuidRe = regexp.MustCompile(
	`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

// IsUUID reports whether value matches the 8-4-4-4-12 hex UUID format.
func IsUUID(value string) bool {
	return uuidRe.MatchString(value)
}

// ResolveSessionID maps a non-UUID display name to its real UUID. The live
// process table is consulted first; otherwise each `.jsonl` transcript in
// projectDir is scanned and a session's current title is the customTitle of the
// LAST "custom-title" line carrying a customTitle key. Returns (uuid, true) only
// for exactly one current match; ("", false) for no match, for an ambiguous tie,
// and for a missing project directory. Malformed JSON lines and over-long lines
// are skipped.
func ResolveSessionID(
	ctx context.Context,
	displayName, projectDir string,
	liveSessionNames map[string]string,
) (string, bool) {
	if sessionID, ok := liveSessionNames[displayName]; ok {
		glog.V(4).Infof(
			"[SessionResolver] Resolved live '%s' -> '%s' from process table",
			displayName,
			sessionID,
		)
		return sessionID, true
	}

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		glog.V(4).Infof("[SessionResolver] project_dir does not exist: %s", projectDir)
		return "", false
	}

	candidates := []string{}
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return "", false
		default:
		}
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		path := filepath.Join(projectDir, name)
		currentTitle, ok := lastCustomTitle(ctx, path)
		if ok && currentTitle == displayName {
			candidates = append(candidates, strings.TrimSuffix(name, ".jsonl"))
		}
	}

	if len(candidates) == 1 {
		glog.V(4).Infof("[SessionResolver] Resolved '%s' -> '%s'", displayName, candidates[0])
		return candidates[0], true
	}
	if len(candidates) > 1 {
		glog.Warningf(
			"[SessionResolver] Ambiguous title '%s' matches %d sessions: %v — refusing to resolve",
			displayName,
			len(candidates),
			candidates,
		)
	}
	return "", false
}

// lastCustomTitle returns the current title of a transcript: the customTitle of
// the last "custom-title" line that carries a customTitle key. A custom-title
// line without a customTitle key does not erase the previous title. A raw line
// longer than maxLineBytes, a malformed JSON line, and an unreadable file each
// contribute nothing.
func lastCustomTitle(ctx context.Context, path string) (string, bool) {
	fh, err := os.Open(path)
	if err != nil {
		glog.Warningf("[SessionResolver] Cannot read %s: %v", path, err)
		return "", false
	}
	defer func() { _ = fh.Close() }()

	reader := bufio.NewReader(fh)
	var current any

	for {
		select {
		case <-ctx.Done():
			return "", false
		default:
		}

		raw, readErr := reader.ReadBytes('\n')
		if len(raw) > 0 && len(raw) <= maxLineBytes {
			line := strings.TrimSpace(string(raw))
			if line != "" {
				var parsed map[string]any
				if unmarshalErr := json.Unmarshal([]byte(line), &parsed); unmarshalErr != nil {
					glog.Warningf("[SessionResolver] Malformed JSON in %s, skipping line", path)
				} else if parsed["type"] == "custom-title" {
					if title, ok := parsed["customTitle"]; ok && title != nil {
						current = title
					}
				}
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				glog.Warningf("[SessionResolver] Cannot read %s: %v", path, readErr)
			}
			break
		}
	}

	title, ok := current.(string)
	return title, ok
}
