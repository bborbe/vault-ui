// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsasset_test

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/bborbe/vault-ui/pkg/board/jsasset"
)

func loadAsset(t *testing.T) string {
	t.Helper()
	source, err := jsasset.LoadAsset()
	if err != nil {
		t.Fatalf("LoadAsset: %v", err)
	}
	if source == "" {
		t.Fatal("LoadAsset returned empty source")
	}
	return source
}

func TestLoadAsset(t *testing.T) {
	source := loadAsset(t)
	if !strings.Contains(source, "const CARD_CONFIG") {
		t.Fatal("embedded asset does not carry CARD_CONFIG")
	}
}

func TestObjectLiteral(t *testing.T) {
	source := loadAsset(t)
	config, err := jsasset.ObjectLiteral(source, "CARD_CONFIG")
	if err != nil {
		t.Fatalf("ObjectLiteral: %v", err)
	}
	for _, kind := range []string{"task", "goal", "topic"} {
		if _, ok := config[kind]; !ok {
			t.Fatalf("CARD_CONFIG missing kind %q", kind)
		}
	}
	task, err := jsasset.ParseObjectLiteral(config["task"])
	if err != nil {
		t.Fatalf("ParseObjectLiteral(task): %v", err)
	}
	slots, err := jsasset.ParseObjectLiteral(task["slots"])
	if err != nil {
		t.Fatalf("ParseObjectLiteral(slots): %v", err)
	}
	if slots["assignee"] != "true" {
		t.Fatalf("task.assignee slot = %q, want true", slots["assignee"])
	}
	if slots["live"] != "false" {
		t.Fatalf("task.live slot = %q, want false", slots["live"])
	}
	if task["map"] != "mapTaskCard" {
		t.Fatalf("task.map = %q, want mapTaskCard", task["map"])
	}
}

func TestReturnedObject(t *testing.T) {
	source := loadAsset(t)
	mapper, err := jsasset.ReturnedObject(source, "mapTaskCard")
	if err != nil {
		t.Fatalf("ReturnedObject: %v", err)
	}
	if got := jsasset.CollapseWhitespace(mapper["assignee"]); got != "task.assignee || ''" {
		t.Fatalf("assignee = %q", got)
	}
	if _, ok := mapper["jiraKey"]; !ok {
		t.Fatal("mapTaskCard return missing jiraKey")
	}

	topic, err := jsasset.ReturnedObject(source, "mapTopicCard")
	if err != nil {
		t.Fatalf("ReturnedObject(mapTopicCard): %v", err)
	}
	if topic["assignee"] != "''" {
		t.Fatalf("mapTopicCard assignee = %q, want ''", topic["assignee"])
	}
}

func TestFunctionBody(t *testing.T) {
	source := loadAsset(t)
	body, err := jsasset.FunctionBody(source, "createCard")
	if err != nil {
		t.Fatalf("FunctionBody: %v", err)
	}
	if !strings.Contains(body, "config.map(item)") {
		t.Fatal("createCard body does not map the item")
	}
	navigate, err := jsasset.FunctionBody(source, "navigateToBlocker")
	if err != nil {
		t.Fatalf("FunctionBody(navigateToBlocker): %v", err)
	}
	if !strings.Contains(navigate, "blocked-target") {
		t.Fatal("navigateToBlocker body does not mark the target")
	}
}

func TestBranch(t *testing.T) {
	source := loadAsset(t)
	branch, err := jsasset.Branch(source, "createCard", "config.slots.assignee")
	if err != nil {
		t.Fatalf("Branch: %v", err)
	}
	if !strings.Contains(branch, "currentAssignees.includes") {
		t.Fatal("assign-control branch does not read the active filter")
	}
	if !strings.Contains(branch, "assign-to-me-link") {
		t.Fatal("assign-control branch does not render the assign control")
	}
}

func TestBranchIgnoresCommentsAndStrings(t *testing.T) {
	source := loadAsset(t)
	// "own row" appears only in a createCard comment.
	if _, err := jsasset.Branch(source, "createCard", "own row"); !stderrors.Is(err, jsasset.ErrNotFound) {
		t.Fatalf("Branch matched a comment: %v", err)
	}
}

func TestMissingConstruct(t *testing.T) {
	source := loadAsset(t)
	if _, err := jsasset.ObjectLiteral(source, "NOPE"); !stderrors.Is(err, jsasset.ErrNotFound) {
		t.Fatalf("ObjectLiteral(NOPE) = %v, want ErrNotFound", err)
	}
	if _, err := jsasset.FunctionBody(source, "nope"); !stderrors.Is(err, jsasset.ErrNotFound) {
		t.Fatalf("FunctionBody(nope) = %v, want ErrNotFound", err)
	}
	if _, err := jsasset.ReturnedObject(source, "nope"); !stderrors.Is(err, jsasset.ErrNotFound) {
		t.Fatalf("ReturnedObject(nope) = %v, want ErrNotFound", err)
	}
	if _, err := jsasset.Branch(source, "createCard", "not-present-anywhere"); !stderrors.Is(err, jsasset.ErrNotFound) {
		t.Fatalf("Branch(absent) = %v, want ErrNotFound", err)
	}
}

func TestParseObjectLiteral(t *testing.T) {
	parsed, err := jsasset.ParseObjectLiteral(`{
		"quoted-key": 'single',
		bare: { nested: true, list: [1, 2, 3] },
		call: fn(a, b),
		template: ` + "`x ${y ? 1 : 2}`" + `,
		trailing: false,
	}`)
	if err != nil {
		t.Fatalf("ParseObjectLiteral: %v", err)
	}
	if parsed["quoted-key"] != "'single'" {
		t.Fatalf("quoted-key = %q", parsed["quoted-key"])
	}
	if parsed["bare"] != "{ nested: true, list: [1, 2, 3] }" {
		t.Fatalf("bare = %q", parsed["bare"])
	}
	if parsed["call"] != "fn(a, b)" {
		t.Fatalf("call = %q", parsed["call"])
	}
	if parsed["template"] != "`x ${y ? 1 : 2}`" {
		t.Fatalf("template = %q", parsed["template"])
	}
	if parsed["trailing"] != "false" {
		t.Fatalf("trailing = %q", parsed["trailing"])
	}
}

func TestParseObjectLiteralMalformed(t *testing.T) {
	if _, err := jsasset.ParseObjectLiteral("not an object"); !stderrors.Is(err, jsasset.ErrMalformed) {
		t.Fatalf("ParseObjectLiteral = %v, want ErrMalformed", err)
	}
	if _, err := jsasset.ParseObjectLiteral("{ unterminated: true"); !stderrors.Is(err, jsasset.ErrMalformed) {
		t.Fatalf("ParseObjectLiteral = %v, want ErrMalformed", err)
	}
}

func TestCollapseWhitespace(t *testing.T) {
	if got := jsasset.CollapseWhitespace("  a \n\t b   c "); got != "a b c" {
		t.Fatalf("CollapseWhitespace = %q", got)
	}
}
