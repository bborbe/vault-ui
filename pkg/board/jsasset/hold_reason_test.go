// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsasset_test

import (
	"strings"
	"testing"

	"github.com/bborbe/vault-ui/pkg/board/jsasset"
)

// TestHoldReasonGates pins when the claude-wrapper hold badge renders: only on a
// flagged, open card carrying a hold_reason, and never when the card already
// says the same thing — its own Live badge for not-absent, its own blocked badge
// for blocked.
func TestHoldReasonGates(t *testing.T) {
	source := loadAsset(t)
	body, err := jsasset.FunctionBody(source, "holdReasonHtml")
	if err != nil {
		t.Fatalf("FunctionBody(holdReasonHtml): %v", err)
	}
	for _, want := range []string{
		"item.hold_reason",
		"!item.flag",
		"isTerminalStatus(item.status)",
		"hold.reason === 'not-absent' && item.session_state === 'live'",
		"hold.reason === 'blocked' && item.blocked",
		"escapeHtml(hold.label",
		"escapeHtml(hold.summary)",
		"safeHttpUrl(hold.pr_url)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("holdReasonHtml lost %q", want)
		}
	}
}

// TestHoldReasonRendersOnCardWithResume pins that the badge is rendered by the
// one card template on its own row and that the session control — the Resume
// button — is still rendered alongside it.
func TestHoldReasonRendersOnCardWithResume(t *testing.T) {
	source := loadAsset(t)
	body, err := jsasset.FunctionBody(source, "createCard")
	if err != nil {
		t.Fatalf("FunctionBody(createCard): %v", err)
	}
	for _, want := range []string{
		"holdReasonHtml(item)",
		"card-hold-reason",
		"${holdReasonRow}",
		"holdReasonTitle(item.hold_reason)",
		"sessionButtonHtml(kind, item)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("createCard lost %q", want)
		}
	}
}

// TestSafeHttpUrlRejectsOtherSchemes pins that a wrapper-supplied PR link can
// only become an http(s) href.
func TestSafeHttpUrlRejectsOtherSchemes(t *testing.T) {
	source := loadAsset(t)
	body, err := jsasset.FunctionBody(source, "safeHttpUrl")
	if err != nil {
		t.Fatalf("FunctionBody(safeHttpUrl): %v", err)
	}
	if !strings.Contains(body, "parsed.protocol === 'https:' || parsed.protocol === 'http:'") {
		t.Fatal("safeHttpUrl does not restrict the scheme to http(s)")
	}
}
