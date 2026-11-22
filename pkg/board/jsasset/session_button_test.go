// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsasset_test

import (
	"strings"
	"testing"

	"github.com/bborbe/vault-ui/pkg/board/jsasset"
)

// TestSessionButtonStopsStartOnTerminalCard pins the shared completed-card rule:
// a terminal card with no session renders the Done chip, never Start. The branch
// lives in the one shared sessionButtonHtml, so it covers task, goal and topic —
// a terminal card that still carries a session reaches the Resume branch first.
func TestSessionButtonStopsStartOnTerminalCard(t *testing.T) {
	source := loadAsset(t)
	body, err := jsasset.FunctionBody(source, "sessionButtonHtml")
	if err != nil {
		t.Fatalf("FunctionBody(sessionButtonHtml): %v", err)
	}
	termIdx := strings.Index(body, "isTerminalStatus(item.status)")
	startIdx := strings.Index(body, "buttonLabel = '▶ Start'")
	if termIdx < 0 {
		t.Fatal("sessionButtonHtml does not gate the Start fallback on isTerminalStatus")
	}
	if startIdx < 0 {
		t.Fatal("sessionButtonHtml lost its Start fallback branch")
	}
	if termIdx > startIdx {
		t.Fatal("the terminal branch must precede the Start fallback")
	}
	hasIdx := strings.Index(body, "} else if (hasSession) {")
	if hasIdx < 0 || hasIdx > termIdx {
		t.Fatal("the hasSession (Resume) branch must precede the terminal branch, so a completed card with a session still resumes")
	}
	if !strings.Contains(body, "done-chip") {
		t.Fatal("sessionButtonHtml does not render the done-chip for a terminal card")
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
