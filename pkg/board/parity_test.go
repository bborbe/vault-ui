// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board_test

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-cli/pkg/ops"

	"github.com/bborbe/vault-ui/pkg/board"
)

// The instrumentation added for the task-list rebuild path must not change what
// the board serves. This row is the container-side half of the spec's
// payload-parity fence: it pins the decoded GET /api/tasks row shape against an
// explicit field list, so a field added to or removed from api.TaskResponse
// without a deliberate edit here fails a row rather than shipping silently.
var _ = Describe("task-list payload parity", func() {
	It("serves the unchanged task-list payload field set", func() {
		h := newHarness(item("Parity"))

		body, err := h.board.ListTasksBody(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())

		// Decode into raw messages rather than into api.TaskResponse: the point is
		// the field set the wire actually carries, not the struct the board typed.
		var rows []map[string]json.RawMessage
		Expect(json.Unmarshal(body.Identity, &rows)).To(Succeed())
		Expect(rows).NotTo(BeEmpty())

		fields := make([]string, 0, len(rows[0]))
		for field := range rows[0] {
			fields = append(fields, field)
		}
		sort.Strings(fields)

		Expect(fields).To(Equal([]string{
			"activity_date",
			"assignee",
			"blocked",
			"blocked_by",
			"blockers",
			"category",
			"claude_session_id",
			"claude_session_started",
			"completed_date",
			"defer_date",
			"description",
			"due_date",
			"flag",
			"goals",
			"hold_reason",
			"id",
			"jira",
			"jump_pane",
			"location",
			"mode",
			"modified_date",
			"obsidian_url",
			"open_questions",
			"phase",
			"planned_date",
			"priority",
			"project_path",
			"recently_completed",
			"recurring",
			"session_state",
			"status",
			"title",
			"upcoming",
			"vault",
		}))
	})
})

// The Tasks, Goals and Topics list paths must apply one deferred-visibility
// rule. A drift between them is invisible on a board whose vault carries no
// deferred rows, so each path is pinned here against the same four cases:
// inside the upcoming window, in the past, far beyond it, and undated.
var _ = Describe("deferred-visibility parity", func() {
	// The harness clock is fixed at baseTime, so the fixtures are dated
	// relative to it rather than to the wall clock.
	now := baseTime
	fixtures := []struct {
		name     string
		deferVal string
		visible  bool
		upcoming bool
	}{
		{"InsideWindow", now.Add(2 * time.Hour).Format(time.RFC3339), true, true},
		{"Past", now.Add(-24 * time.Hour).Format(time.RFC3339), true, false},
		{"FarFuture", now.Add(30 * 24 * time.Hour).Format(time.RFC3339), false, false},
		{"Undated", "", true, false},
	}

	items := func() []ops.TaskListItem {
		out := make([]ops.TaskListItem, 0, len(fixtures))
		for _, f := range fixtures {
			out = append(out, ops.TaskListItem{
				Name:      f.name,
				Status:    "in_progress",
				DeferDate: f.deferVal,
			})
		}
		return out
	}

	It("applies the same four outcomes to tasks, goals and topics", func() {
		// Tasks are seeded through the constructor: the task path reads a
		// published-rows snapshot taken at harness build, so a post-build map
		// assignment would not reach it. Goals and topics read the list directly.
		h := newHarness(items()...)
		h.list.items["23 Goals"] = items()
		h.list.items["23 Topics"] = items()

		body, err := h.board.ListTasksBody(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		var taskRows []struct {
			ID       string `json:"id"`
			Upcoming bool   `json:"upcoming"`
		}
		Expect(json.Unmarshal(body.Identity, &taskRows)).To(Succeed())

		goals, err := h.board.ListGoals(context.Background(), board.GoalQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		topics, err := h.board.ListTopics(context.Background(), board.TopicQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())

		taskByID := map[string]bool{}
		for _, row := range taskRows {
			taskByID[row.ID] = row.Upcoming
		}
		goalByID := map[string]bool{}
		for _, goal := range goals {
			goalByID[goal.ID] = goal.Upcoming
		}
		topicByID := map[string]bool{}
		for _, topic := range topics {
			topicByID[topic.ID] = topic.Upcoming
		}

		for label, byID := range map[string]map[string]bool{
			"tasks":  taskByID,
			"goals":  goalByID,
			"topics": topicByID,
		} {
			for _, f := range fixtures {
				upcoming, present := byID[f.name]
				if f.visible {
					Expect(present).To(BeTrue(), "%s: %s should be visible", label, f.name)
					Expect(upcoming).To(Equal(f.upcoming), "%s: %s upcoming flag", label, f.name)
					continue
				}
				Expect(present).To(BeFalse(), "%s: %s should be hidden", label, f.name)
			}
		}
	})

	It("serves a goal whose defer arrives in the vault-api's Go-time form", func() {
		h := newHarness()
		h.list.items["23 Goals"] = []ops.TaskListItem{{
			Name:      "GoTimeGoal",
			Status:    "in_progress",
			DeferDate: "2026-08-27 00:00:00 +0000 UTC",
		}}

		goals, err := h.board.ListGoals(context.Background(), board.GoalQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(goals).To(HaveLen(1))
		Expect(goals[0].DeferDate).NotTo(BeNil())
		Expect(*goals[0].DeferDate).To(Equal("2026-08-27 00:00:00 +0000 UTC"))
	})
})
