// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsasset_test

import (
	"strings"
	"testing"

	"github.com/bborbe/vault-ui/pkg/board/jsasset"
)

// cardEntry parses one kind's CARD_CONFIG entry from the embedded asset.
func cardEntry(t *testing.T, source, kind string) map[string]string {
	t.Helper()
	config, err := jsasset.ObjectLiteral(source, "CARD_CONFIG")
	if err != nil {
		t.Fatalf("ObjectLiteral(CARD_CONFIG): %v", err)
	}
	entry, err := jsasset.ParseObjectLiteral(config[kind])
	if err != nil {
		t.Fatalf("ParseObjectLiteral(%s): %v", kind, err)
	}
	return entry
}

func topicSlots(t *testing.T, source string) map[string]string {
	t.Helper()
	slots, err := jsasset.ParseObjectLiteral(cardEntry(t, source, "topic")["slots"])
	if err != nil {
		t.Fatalf("ParseObjectLiteral(topic.slots): %v", err)
	}
	return slots
}

func topicView(t *testing.T, source string) map[string]string {
	t.Helper()
	view, err := jsasset.ReturnedObject(source, "mapTopicCard")
	if err != nil {
		t.Fatalf("ReturnedObject(mapTopicCard): %v", err)
	}
	return view
}

// TestTopicCardEnablesDataSlots pins the topic card's slot set: it carries the
// six data slots the task and goal cards already render and keeps the four it
// shipped with.
func TestTopicCardEnablesDataSlots(t *testing.T) {
	source := loadAsset(t)
	slots := topicSlots(t, source)
	for _, slot := range []string{"hold", "assignee", "priority", "activityAge", "blocked", "jira"} {
		if slots[slot] != "true" {
			t.Fatalf("topic.%s slot = %q, want true", slot, slots[slot])
		}
	}
	for _, slot := range []string{"noteLink", "flag", "live", "detail"} {
		if slots[slot] != "true" {
			t.Fatalf("topic.%s slot = %q, want true (existing slot must stay enabled)", slot, slots[slot])
		}
	}
}

// TestTopicMapperReadsPayloadFields pins that every view-model key comes from
// the topic payload, not a constant, and that the jira keys flow through the
// shared helper.
func TestTopicMapperReadsPayloadFields(t *testing.T) {
	source := loadAsset(t)
	view := topicView(t, source)
	fields := map[string]string{
		"id":           "topic.id",
		"vault":        "topic.vault",
		"title":        "topic.title",
		"obsidianUrl":  "topic.obsidian_url",
		"status":       "topic.status",
		"phase":        "topic.phase",
		"flag":         "topic.flag",
		"live":         "topic.session_state",
		"hold":         "topic.status",
		"assignee":     "topic.assignee",
		"priority":     "topic.priority",
		"activityDate": "topic.activity_date",
		"blockers":     "topic.blockers",
	}
	for key, field := range fields {
		expr, ok := view[key]
		if !ok {
			t.Fatalf("mapTopicCard return missing %q", key)
		}
		if !strings.Contains(expr, field) {
			t.Fatalf("mapTopicCard %s = %q, want reference to %s", key, expr, field)
		}
	}
	body, err := jsasset.FunctionBody(source, "mapTopicCard")
	if err != nil {
		t.Fatalf("FunctionBody(mapTopicCard): %v", err)
	}
	if !strings.Contains(body, "extractJiraIssue(topic.jira") {
		t.Fatal("mapTopicCard does not derive jiraKey/jiraUrl from the payload's jira value")
	}
	if !strings.Contains(view["jiraKey"], "issueKey") {
		t.Fatalf("mapTopicCard jiraKey = %q, want issueKey", view["jiraKey"])
	}
	if !strings.Contains(view["jiraUrl"], "issueUrl") {
		t.Fatalf("mapTopicCard jiraUrl = %q, want issueUrl", view["jiraUrl"])
	}
}

// TestAssignControlOnlyWhenAssignFunctionSet pins that the assign control is
// emitted in the empty-assignee branch, behind a non-null assign function. A
// kind with a null assign function (the topic) renders no assign control, while
// the non-empty branch keeps the assignee badge.
func TestAssignControlOnlyWhenAssignFunctionSet(t *testing.T) {
	source := loadAsset(t)
	block, err := jsasset.Branch(source, "createCard", "config.slots.assignee")
	if err != nil {
		t.Fatalf("Branch(createCard, config.slots.assignee): %v", err)
	}
	guard := strings.Index(block, "else if (config.assignCall)")
	link := strings.Index(block, "assign-to-me-link")
	if guard < 0 {
		t.Fatalf("empty-assignee branch is not guarded on config.assignCall: %q", block)
	}
	if link < 0 || link < guard {
		t.Fatalf("assign control is not emitted inside the guarded empty-assignee branch: %q", block)
	}
	if strings.Contains(block[:guard], "assign-to-me-link") {
		t.Fatal("assign control is emitted outside the guarded empty-assignee branch")
	}

	if got := cardEntry(t, source, "topic")["assignCall"]; got != "null" {
		t.Fatalf("topic.assignCall = %q, want null", got)
	}
	if got := cardEntry(t, source, "task")["assignCall"]; got == "null" {
		t.Fatal("task.assignCall is null, want a function")
	}
	if got := cardEntry(t, source, "goal")["assignCall"]; got == "null" {
		t.Fatal("goal.assignCall is null, want a function")
	}
}

// TestJiraSlotAnchorUsesMappedURL pins the jira slot's markup shape: the anchor
// href is the mapper's derived URL, so an absent URL renders no badge.
func TestJiraSlotAnchorUsesMappedURL(t *testing.T) {
	source := loadAsset(t)
	body, err := jsasset.FunctionBody(source, "createCard")
	if err != nil {
		t.Fatalf("FunctionBody(createCard): %v", err)
	}
	for _, want := range []string{
		`<div class="card-jira">`,
		`<a href="${view.jiraUrl}" class="jira-badge"`,
		`config.slots.jira && view.jiraKey && view.jiraUrl`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("createCard jira slot missing %q", want)
		}
	}
}

// TestTopicJiraURLFallsBackToEmpty pins the jira URL chain: the mapper's URL is
// the empty-string fallback of the value extractJiraIssue derives, and the
// helper's domain lookup stays gated on the fixed project-to-domain map. A key
// whose project is absent from that map therefore yields an empty URL, and an
// empty URL renders no badge.
func TestTopicJiraURLFallsBackToEmpty(t *testing.T) {
	source := loadAsset(t)
	view := topicView(t, source)
	if got := jsasset.CollapseWhitespace(view["jiraUrl"]); got != "issueUrl || ''" {
		t.Fatalf("mapTopicCard jiraUrl = %q, want issueUrl || ''", got)
	}
	body, err := jsasset.FunctionBody(source, "mapTopicCard")
	if err != nil {
		t.Fatalf("FunctionBody(mapTopicCard): %v", err)
	}
	if !strings.Contains(body, "extractJiraIssue(topic.jira") {
		t.Fatal("mapTopicCard jiraUrl does not come from extractJiraIssue")
	}

	helper, err := jsasset.FunctionBody(source, "extractJiraIssue")
	if err != nil {
		t.Fatalf("FunctionBody(extractJiraIssue): %v", err)
	}
	for _, want := range []string{
		"projectDomains",
		"const domain = projectDomains[project]",
		"domain ?",
		"'BRO': 'seibertgroup.atlassian.net'",
		"'TRADE': 'borbe.atlassian.net'",
	} {
		if !strings.Contains(helper, want) {
			t.Fatalf("extractJiraIssue domain lookup missing %q", want)
		}
	}
	// The map is fixed: only the two known projects resolve to a domain, so an
	// unknown project falls through to the null URL.
	if got := strings.Count(helper, "atlassian.net"); got != 2 {
		t.Fatalf("extractJiraIssue carries %d domains, want the fixed 2", got)
	}
}

// TestBlockerResolverUsesTopicDataset pins that a topic resolves its blocker
// against the topic id dataset rather than falling through to the task dataset.
func TestBlockerResolverUsesTopicDataset(t *testing.T) {
	source := loadAsset(t)
	body, err := jsasset.FunctionBody(source, "navigateToBlocker")
	if err != nil {
		t.Fatalf("FunctionBody(navigateToBlocker): %v", err)
	}
	if !strings.Contains(body, "kind === 'topic'") {
		t.Fatal("navigateToBlocker does not branch on the topic kind")
	}
	if !strings.Contains(body, "card.dataset.topicId === name") {
		t.Fatal("navigateToBlocker does not resolve a topic blocker against topicId")
	}
}

// TestTopicHoldComparesStatus pins the hold flag to a status comparison rather
// than a constant or a truthiness test.
func TestTopicHoldComparesStatus(t *testing.T) {
	source := loadAsset(t)
	hold := jsasset.CollapseWhitespace(topicView(t, source)["hold"])
	if hold != "topic.status === 'hold'" {
		t.Fatalf("mapTopicCard hold = %q, want topic.status === 'hold'", hold)
	}
}
