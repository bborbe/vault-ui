// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsasset_test

import (
	"reflect"
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
// six data slots the task and goal cards already render and keeps the three of
// its original four that remain. The display-only `live` slot is gone: a live
// topic now renders the same clickable take-over badge as a task or goal.
func TestTopicCardEnablesDataSlots(t *testing.T) {
	source := loadAsset(t)
	slots := topicSlots(t, source)
	for _, slot := range []string{"hold", "assignee", "priority", "activityAge", "blocked", "jira"} {
		if slots[slot] != "true" {
			t.Fatalf("topic.%s slot = %q, want true", slot, slots[slot])
		}
	}
	for _, slot := range []string{"noteLink", "flag", "detail"} {
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

// TestCardConfigSlotParityAcrossKinds pins the three kinds' card slots to one
// contract. goal and task are the same view in two tabs, so they carry an
// identical slot map. topic differs only by the two slots pinned below, each
// deliberate; every other slot — and any slot added to task later — must appear
// on topic too, so a one-entry edit can no longer drift silently.
func TestCardConfigSlotParityAcrossKinds(t *testing.T) {
	source := loadAsset(t)
	task := cardSlots(t, source, "task")
	goal := cardSlots(t, source, "goal")
	topic := cardSlots(t, source, "topic")

	if !reflect.DeepEqual(task, goal) {
		t.Fatalf("goal.slots differs from task.slots: task=%v goal=%v", task, goal)
	}

	// The pinned divergence, with the value each must hold.
	pinned := map[string]string{
		"menu":   "false", // no PATCH /api/topics/{id}/status route yet
		"detail": "true",  // topic-only detail view
	}

	for name, taskValue := range task {
		topicValue, ok := topic[name]
		if !ok {
			t.Fatalf("topic.slots is missing %q carried by task.slots — a slot added to task alone must not skip topics", name)
		}
		if want, isPinned := pinned[name]; isPinned {
			if topicValue != want {
				t.Fatalf("topic.slots.%s = %q, want the pinned %q", name, topicValue, want)
			}
			continue
		}
		if topicValue != taskValue {
			t.Fatalf("topic.slots.%s = %q, want %q — only menu and detail may differ", name, topicValue, taskValue)
		}
	}
	for name := range topic {
		if _, ok := task[name]; !ok {
			t.Fatalf("topic.slots carries %q absent from task.slots — the divergence is pinned to menu and detail", name)
		}
	}

	if got := cardEntry(t, source, "topic")["assigneeClickable"]; got != "false" {
		t.Fatalf("topic.assigneeClickable = %q, want false (no topic assign route)", got)
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

// TestCardConfigEnablesModeSlot pins the mode slot on for all three kinds and
// keeps every slot each kind carried before it. The pre-change lists are
// hardcoded on purpose: this same change extends the baseline fixture, so
// reading the expectation back from it would compare the change to itself.
func TestCardConfigEnablesModeSlot(t *testing.T) {
	preChangeEnabledSlots := map[string][]string{
		"task":  {"activityAge", "assignee", "blocked", "flag", "hold", "jira", "menu", "noteLink", "priority", "session"},
		"goal":  {"activityAge", "assignee", "blocked", "flag", "hold", "jira", "menu", "noteLink", "priority", "session"},
		"topic": {"activityAge", "assignee", "blocked", "detail", "flag", "hold", "jira", "noteLink", "priority"},
	}

	source := loadAsset(t)
	for kind, preChange := range preChangeEnabledSlots {
		slots := cardSlots(t, source, kind)
		if slots["mode"] != "true" {
			t.Fatalf("%s: mode slot is %q, want \"true\"", kind, slots["mode"])
		}
		for _, name := range preChange {
			if slots[name] != "true" {
				t.Fatalf("%s: pre-change slot %q is no longer enabled", kind, name)
			}
		}
	}
}

// TestCardMappersReadModeFromPayload pins each mapper's mode expression to its
// own payload field. A constant, a fallback or a coerced boolean all fail.
func TestCardMappersReadModeFromPayload(t *testing.T) {
	cases := []struct {
		kind     string
		mapperFn string
		field    string
	}{
		{"task", "mapTaskCard", "task.mode"},
		{"goal", "mapGoalCard", "goal.mode"},
		{"topic", "mapTopicCard", "topic.mode"},
	}

	source := loadAsset(t)
	for _, tc := range cases {
		view, err := jsasset.ReturnedObject(source, tc.mapperFn)
		if err != nil {
			t.Fatalf("ReturnedObject(%s): %v", tc.mapperFn, err)
		}
		if got := jsasset.CollapseWhitespace(view["mode"]); got != tc.field {
			t.Fatalf("%s: mode expression is %q, want %q", tc.kind, got, tc.field)
		}
	}

	cardFn, err := jsasset.FunctionBody(source, "createCard")
	if err != nil {
		t.Fatalf("FunctionBody(createCard): %v", err)
	}
	if !strings.Contains(cardFn, "config.slots.mode && view.mode") {
		t.Fatalf("createCard does not gate the mode pill on slot AND value")
	}
}

// TestModeBadgeNamesModeAndEscapes asserts the pill's three helpers structurally.
//
// AC6's two adversarial payloads cannot be rendered here — this repo has no JS
// runtime — so the coverage is structural, and the rendered proof is the spec's
// post-deploy browser check:
//   - `<img src=x onerror=alert(1)>` cannot become an element because the label
//     path goes through escapeHtml.
//   - `x" onmouseover="alert(1)` cannot split an attribute because the markup
//     carries no attribute at all and the title path interpolates nothing.
func TestModeBadgeNamesModeAndEscapes(t *testing.T) {
	source := loadAsset(t)

	body := func(name string) string {
		t.Helper()
		fn, err := jsasset.FunctionBody(source, name)
		if err != nil {
			t.Fatalf("FunctionBody(%s): %v", name, err)
		}
		return fn
	}

	label := body("modeBadgeLabel")
	for _, want := range []string{"'⚙ headless'", "'🖥 interactive'", "return mode;"} {
		if !strings.Contains(label, want) {
			t.Fatalf("modeBadgeLabel is missing %q — a two-case switch would drop an unknown mode", want)
		}
	}

	title := body("modeBadgeTitle")
	for _, want := range []string{
		"'Starts a headless worker (no UI)'",
		"'Opens an interactive session'",
		"'Execution mode'",
	} {
		if !strings.Contains(title, want) {
			t.Fatalf("modeBadgeTitle is missing %q", want)
		}
	}
	if strings.Contains(title, "`${") {
		t.Fatalf("modeBadgeTitle builds its title from the value; it must return constants only")
	}

	html := body("modeBadgeHtml")
	for _, want := range []string{`class="mode-badge"`, "escapeHtml(", "modeBadgeLabel(mode)"} {
		if !strings.Contains(html, want) {
			t.Fatalf("modeBadgeHtml is missing %q", want)
		}
	}
	if strings.Contains(html, "title=") {
		t.Fatalf("modeBadgeHtml carries a title attribute; the title must be assigned through the DOM")
	}

	card := body("createCard")
	for _, want := range []string{"querySelector('.mode-badge')", ".title = modeBadgeTitle(view.mode)"} {
		if !strings.Contains(card, want) {
			t.Fatalf("createCard is missing %q", want)
		}
	}
}

// TestCardConfigGreysTerminalCardsOnGoalAndTopic pins the dimming contract. The
// task greys through the server's `recently_completed` flag, which only covers
// the last 8 hours, so its kind must not also grey on status — a second rule
// there would grey rows the backend deliberately leaves out. The goal and topic
// responses carry no such flag, so those kinds opt in on their own terminal
// status. One shared class and one shared predicate, so the Done lane reads the
// same on all three boards.
func TestCardConfigGreysTerminalCardsOnGoalAndTopic(t *testing.T) {
	source := loadAsset(t)

	want := map[string]string{"task": "false", "goal": "true", "topic": "true"}
	for kind, value := range want {
		if got := cardEntry(t, source, kind)["greyWhenTerminal"]; got != value {
			t.Fatalf("%s.greyWhenTerminal = %q, want %q", kind, got, value)
		}
	}

	body, err := jsasset.FunctionBody(source, "createCard")
	if err != nil {
		t.Fatalf("FunctionBody(createCard): %v", err)
	}
	guard := jsasset.CollapseWhitespace(body)
	for _, want := range []string{
		"view.recentlyCompleted || (config.greyWhenTerminal && isTerminalStatus(view.status))",
		"card.classList.add('recently-completed')",
	} {
		if !strings.Contains(guard, want) {
			t.Fatalf("createCard is missing %q", want)
		}
	}

	// The status predicate is the ✓ Done chip's own rule rather than a second
	// copy of it: a card that shows the chip must be the card that greys out.
	terminal, err := jsasset.FunctionBody(source, "isTerminalStatus")
	if err != nil {
		t.Fatalf("FunctionBody(isTerminalStatus): %v", err)
	}
	for _, status := range []string{"'completed'", "'aborted'"} {
		if !strings.Contains(terminal, status) {
			t.Fatalf("isTerminalStatus no longer covers %s — the grey rule and the ✓ Done chip would diverge", status)
		}
	}
}
