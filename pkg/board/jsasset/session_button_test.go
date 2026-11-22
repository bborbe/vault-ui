// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsasset_test

import (
	"strings"
	"testing"

	"github.com/bborbe/vault-ui/pkg/board/jsasset"
)

// TestSessionButtonGatesTerminalCardFirst pins the shared completed-card rule:
// a terminal card offers no session control at all — no Resume, no Live, no
// Starting, no Start. The gate sits at the top of the one shared
// sessionButtonHtml, so it covers task, goal and topic, and it must precede
// every open-card branch: a closed row that still carries a session id must
// not reach Resume, and one with a stale live or starting signal must not
// reach those badges either.
func TestSessionButtonGatesTerminalCardFirst(t *testing.T) {
	source := loadAsset(t)
	body, err := jsasset.FunctionBody(source, "sessionButtonHtml")
	if err != nil {
		t.Fatalf("FunctionBody(sessionButtonHtml): %v", err)
	}
	termIdx := strings.Index(body, "isTerminalStatus(item.status)")
	if termIdx < 0 {
		t.Fatal("sessionButtonHtml does not gate on isTerminalStatus")
	}
	if !strings.Contains(body, "done-chip") {
		t.Fatal("sessionButtonHtml does not render the done-chip for a terminal card")
	}
	for _, branch := range []struct{ name, marker string }{
		{"Starting", "if (isStarting) {"},
		{"Live", "item.session_state === 'live'"},
		{"Resume", "} else if (hasSession) {"},
		{"Start", "buttonLabel = '▶ Start'"},
	} {
		idx := strings.Index(body, branch.marker)
		if idx < 0 {
			t.Fatalf("sessionButtonHtml lost its %s branch", branch.name)
		}
		if termIdx > idx {
			t.Fatalf("the terminal gate must precede the %s branch, so a closed card renders no session control", branch.name)
		}
	}
}

// TestIsTerminalStatusCoversCompletedAndAborted pins the terminal set the shared
// button keys on. `hold` is deliberately absent: a held card may still resume.
func TestIsTerminalStatusCoversCompletedAndAborted(t *testing.T) {
	source := loadAsset(t)
	body, err := jsasset.FunctionBody(source, "isTerminalStatus")
	if err != nil {
		t.Fatalf("FunctionBody(isTerminalStatus): %v", err)
	}
	for _, want := range []string{"'completed'", "'aborted'"} {
		if !strings.Contains(body, want) {
			t.Fatalf("isTerminalStatus does not cover %s", want)
		}
	}
	if strings.Contains(body, "'hold'") {
		t.Fatal("isTerminalStatus must not treat hold as terminal")
	}
}
