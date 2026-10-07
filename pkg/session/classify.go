// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	"context"
	"slices"
	"time"

	libtime "github.com/bborbe/time"

	"github.com/bborbe/vault-ui/pkg/activity"
)

// ClassifyParams carries one classification request. ResumeSessionIDs and
// RegistrySessionIDs are computed by the caller (from ProcessTable and
// activity.ReadRegistrySessionIDs); ClassifySessionState performs no ps scan and
// no registry read itself.
type ClassifyParams struct {
	SessionID          string
	ProjectDir         string
	ProjectsRoot       string
	Now                libtime.DateTime
	LiveWindow         time.Duration
	ResumeSessionIDs   []string
	RegistrySessionIDs []string

	// TranscriptMtime probes the session transcript; nil means
	// activity.TranscriptMtime. The board passes its cached probe so a request
	// never touches the filesystem.
	TranscriptMtime activity.TranscriptMtimeGetter
}

// ClassifySessionState reproduces src/vault_ui/activity.py classify_session_state.
//
// The three signals are checked in a fixed order: the harness session registry
// first (authoritative), then transcript recency within LiveWindow, then a live
// `--resume`/`--session-id` process cross-check. The task-file mtime is never a
// liveness signal. A session id with a transcript that cannot be found reads
// indeterminate, never quiet.
func ClassifySessionState(ctx context.Context, params ClassifyParams) SessionState {
	if params.SessionID == "" {
		return SessionStateNone
	}
	if slices.Contains(params.RegistrySessionIDs, params.SessionID) {
		return SessionStateLive
	}

	probe := params.TranscriptMtime
	if probe == nil {
		probe = activity.TranscriptMtime
	}
	mtime := probe(ctx, params.SessionID, params.ProjectDir, params.ProjectsRoot)
	if mtime == nil {
		return SessionStateIndeterminate
	}

	now := params.Now.UTC()
	if now.Sub(*mtime).Duration() <= params.LiveWindow {
		return SessionStateLive
	}

	if slices.Contains(params.ResumeSessionIDs, params.SessionID) {
		return SessionStateLive
	}
	return SessionStateQuiet
}
