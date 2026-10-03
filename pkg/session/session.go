// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package session reproduces the Python vault_ui.activity session-state
// classification and process-table parsing as a behavior-preserving port.
package session

import "time"

// SessionState is the classification of a card's Claude session.
// The empty value is the Python None: the card carries no claude_session_id.
type SessionState string

const (
	// SessionStateNone means the card carries no session id; nothing to classify.
	SessionStateNone SessionState = ""
	// SessionStateLive means a session is running right now.
	SessionStateLive SessionState = "live"
	// SessionStateQuiet means the session ended and a Resume is safe.
	SessionStateQuiet SessionState = "quiet"
	// SessionStateIndeterminate means a session id is set but no transcript can
	// be found, so the session cannot be proven dead.
	SessionStateIndeterminate SessionState = "indeterminate"
)

// DefaultLiveWindow is the transcript-recency window from the Python LIVE_WINDOW
// (five minutes). It is the default; the value is always passed explicitly to
// ClassifySessionState so tests and callers can vary it.
const DefaultLiveWindow = 5 * time.Minute
