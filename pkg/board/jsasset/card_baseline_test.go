// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsasset_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/bborbe/vault-ui/pkg/board/jsasset"
)

// baselinePath is the pre-change card vector captured before the topic card
// work. It is read-only here: this test proves the task and goal cards did not
// move, it never rewrites the baseline.
var baselinePath = filepath.Join("..", "testdata", "card-vectors-pre-change.json")

type cardVector struct {
	EnabledSlots []string          `json:"enabled_slots"`
	Mapper       map[string]string `json:"mapper"`
}

func loadBaseline(t *testing.T) map[string]cardVector {
	t.Helper()
	data, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read baseline %s: %v", baselinePath, err)
	}
	var baseline map[string]cardVector
	if err := json.Unmarshal(data, &baseline); err != nil {
		t.Fatalf("decode baseline %s: %v", baselinePath, err)
	}
	return baseline
}

// cardSlots parses one kind's slots object out of the embedded CARD_CONFIG.
func cardSlots(t *testing.T, source, kind string) map[string]string {
	t.Helper()
	slots, err := jsasset.ParseObjectLiteral(cardEntry(t, source, kind)["slots"])
	if err != nil {
		t.Fatalf("ParseObjectLiteral(%s.slots): %v", kind, err)
	}
	return slots
}

// TestTaskAndGoalCardDefinitionsMatchBaseline locks the task and goal card
// definitions to the pre-change baseline: the enabled slot set and the full
// key-to-source-expression map of each mapper must match exactly.
//
// The topic entry and mapTopicCard are deliberately excluded. The preceding
// change enables six data slots on the topic card and rewrites its mapper, so
// comparing them against the pre-change baseline would make this test
// unsatisfiable. Do not "fix" this omission by adding them.
func TestTaskAndGoalCardDefinitionsMatchBaseline(t *testing.T) {
	source := loadAsset(t)
	baseline := loadBaseline(t)

	for _, tc := range []struct {
		kind     string
		mapperFn string
	}{
		{kind: "task", mapperFn: "mapTaskCard"},
		{kind: "goal", mapperFn: "mapGoalCard"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			want, ok := baseline[tc.kind]
			if !ok {
				t.Fatalf("baseline is missing the %q entry", tc.kind)
			}

			assertEnabledSlotsMatch(t, tc.kind, want.EnabledSlots, cardSlots(t, source, tc.kind))

			mapper, err := jsasset.ReturnedObject(source, tc.mapperFn)
			if err != nil {
				t.Fatalf("ReturnedObject(%s): %v", tc.mapperFn, err)
			}
			assertMapperMatches(t, tc.kind, tc.mapperFn, want.Mapper, mapper)
		})
	}
}

// assertEnabledSlotsMatch reports the exact slot names that diverge, so the
// failure names the entry and each added or removed slot.
func assertEnabledSlotsMatch(t *testing.T, kind string, want []string, slots map[string]string) {
	t.Helper()
	gotSet := map[string]bool{}
	for name, value := range slots {
		if value == "true" {
			gotSet[name] = true
		}
	}
	wantSet := map[string]bool{}
	for _, name := range want {
		wantSet[name] = true
	}
	var missing, extra []string
	for name := range wantSet {
		if !gotSet[name] {
			missing = append(missing, name)
		}
	}
	for name := range gotSet {
		if !wantSet[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("%s enabled slots diverged from baseline: missing=%v extra=%v", kind, missing, extra)
	}
}

// assertMapperMatches compares keys and expressions. Whitespace is collapsed on
// both sides so a pure reformat does not fail, while any change to the
// expression itself does. The failure names the entry, the mapper and the key.
func assertMapperMatches(t *testing.T, kind, funcName string, want, got map[string]string) {
	t.Helper()
	for _, key := range sortedKeys(want) {
		gotExpr, ok := got[key]
		if !ok {
			t.Fatalf("%s mapper %s is missing key %q (baseline expression %q)", kind, funcName, key, want[key])
		}
		if jsasset.CollapseWhitespace(gotExpr) != jsasset.CollapseWhitespace(want[key]) {
			t.Fatalf("%s mapper %s key %q = %q, want %q", kind, funcName, key, gotExpr, want[key])
		}
	}
	for _, key := range sortedKeys(got) {
		if _, ok := want[key]; !ok {
			t.Fatalf("%s mapper %s has key %q = %q, absent from baseline", kind, funcName, key, got[key])
		}
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
