// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board_test

import (
	"context"
	"encoding/json"
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
			"id",
			"jump_pane",
			"location",
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
