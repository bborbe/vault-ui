// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board_test

import (
	"context"

	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/ops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/board"
)

// taskByID returns the response for one task id, failing the spec when it is
// absent so an assertion never reads a zero value by accident.
func taskByID(responses []api.TaskResponse, id string) api.TaskResponse {
	for _, response := range responses {
		if response.ID == id {
			return response
		}
	}
	Fail("task " + id + " is not in the response")
	return api.TaskResponse{}
}

// pageStatus builds one in-memory vault page carrying a status, so the board's
// row patch can be seen to re-derive the row from the page the index holds.
func pageStatus(name, status string) *domain.Page {
	return domain.NewPage(
		map[string]any{"status": status},
		domain.FileMetadata{Name: name},
		domain.Content(""),
	)
}

var _ = Describe("row patch", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("re-derives only the changed row without touching the vault seams", func() {
		h := newHarness(item("Alpha"))
		h.index.setPages(pageStatus("Alpha", "in_progress"))

		built, err := h.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(built).To(HaveLen(1))
		Expect(built[0].Status).To(Equal("todo"))

		builds := h.counter.get()
		registry := h.signals.registryCalls.get()
		resume := h.signals.resumeCalls.get()
		transcripts := h.sessions.transcriptCalls.get()
		pageLists := h.pageIndex.ListPagesCallCount()
		indexReads := h.index.PageReads()

		h.index.markPages("Alpha.md")
		patched, err := h.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(patched).To(HaveLen(1))
		Expect(patched[0].ID).To(Equal("Alpha"))
		Expect(patched[0].Status).To(Equal("in_progress"))

		// No vault list operation, no session-id spawn, no transcript probe and
		// no vault-wide page scan: the patch reads only the pages the index
		// holds, in one bounded snapshot read that resolves the write mark.
		Expect(h.counter.get()).To(Equal(builds))
		Expect(h.signals.registryCalls.get()).To(Equal(registry))
		Expect(h.signals.resumeCalls.get()).To(Equal(resume))
		Expect(h.sessions.transcriptCalls.get()).To(Equal(transcripts))
		Expect(h.pageIndex.ListPagesCallCount()).To(Equal(pageLists))
		Expect(h.pageIndex.ReadPageCallCount()).To(Equal(0))
		Expect(h.index.PageReads()).To(Equal(indexReads + 1))

		// The row the first read returned is untouched.
		Expect(built[0].Status).To(Equal("todo"))
	})

	It("carries the held session fields onto the re-derived row", func() {
		h := newHarness(item("Alpha", func(i *ops.TaskListItem) {
			i.ClaudeSessionID = "11111111-1111-1111-1111-111111111111"
		}))
		h.signals.registry = []string{"11111111-1111-1111-1111-111111111111"}
		h.index.setPages(pageStatus("Alpha", "in_progress"))

		built, err := h.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(built[0].SessionState).NotTo(BeNil())
		held := *built[0].SessionState

		h.index.markPages("Alpha.md")
		patched, err := h.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(patched[0].Status).To(Equal("in_progress"))
		Expect(patched[0].SessionState).NotTo(BeNil())
		Expect(*patched[0].SessionState).To(Equal(held))
	})

	It("drops the row of a changed page that is gone", func() {
		h := newHarness(item("Alpha"), item("Beta"))
		h.index.setPages(pageStatus("Alpha", "todo"))

		built, err := h.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(built).To(HaveLen(2))
		builds := h.counter.get()

		h.index.markPages("Beta.md")
		patched, err := h.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(patched).To(HaveLen(1))
		Expect(patched[0].ID).To(Equal("Alpha"))
		Expect(h.counter.get()).To(Equal(builds))
	})

	It("falls back to the full build for a changed page with no held row", func() {
		h := newHarness(item("Alpha"))

		_, err := h.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		builds := h.counter.get()

		h.index.setPages(pageStatus("Alpha", "todo"), pageStatus("New", "todo"))
		h.list.items["24 Tasks"] = []ops.TaskListItem{item("Alpha"), item("New")}
		h.index.markPages("New.md")

		patched, err := h.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(patched).To(HaveLen(2))
		Expect(h.counter.get()).To(Equal(builds + 1))
	})

	It("rebuilds in full for a folder-level mark", func() {
		h := newHarness(item("Alpha"))
		h.index.setPages(pageStatus("Alpha", "in_progress"))

		_, err := h.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		builds := h.counter.get()

		h.index.bump()
		rebuilt, err := h.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(rebuilt).To(HaveLen(1))
		Expect(rebuilt[0].Status).To(Equal("todo"))
		Expect(h.counter.get()).To(Equal(builds + 1))
	})

	It("re-derives the Open Questions of a changed page from its own content", func() {
		h := newHarness(item("Alpha"))

		_, err := h.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())

		h.index.setPages(page("Alpha", "## Open Questions\n\n1. Which vault?\n"))
		h.index.markPages("Alpha.md")

		patched, err := h.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(patched).To(HaveLen(1))
		Expect(patched[0].OpenQuestions).To(HaveLen(1))
		Expect(patched[0].OpenQuestions[0].Text).To(Equal("Which vault?"))
	})

	It("recomputes a dependent row's blockers when its blocker's page changes", func() {
		h := newHarness(
			item("Alpha"),
			item("Beta", func(i *ops.TaskListItem) { i.BlockedBy = []string{"[[Alpha]]"} }),
		)
		// Beta is blocked by Alpha while Alpha is still open.
		h.cache.statuses["Alpha"] = "in_progress"
		h.index.setPages(pageStatus("Alpha", "in_progress"), pageStatus("Beta", "todo"))

		built, err := h.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(taskByID(built, "Beta").Blocked).To(BeTrue())
		Expect(taskByID(built, "Beta").Blockers).To(Equal([]string{"Alpha"}))
		builds := h.counter.get()

		// Completing Alpha refreshes Alpha's status-cache entry and marks Alpha's
		// page, so the next read takes the row patch rather than a full build.
		h.cache.statuses["Alpha"] = "completed"
		h.index.setPages(pageStatus("Alpha", "completed"), pageStatus("Beta", "todo"))
		h.index.markPages("Alpha.md")

		patched, err := h.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		// Beta's own page did not change, so its row is carried over — but
		// blockers/blocked are cross-row derived and must be recomputed, or Beta
		// keeps showing as blocked by the Alpha it is no longer waiting on.
		Expect(taskByID(patched, "Beta").Blocked).To(BeFalse())
		Expect(taskByID(patched, "Beta").Blockers).To(BeEmpty())
		// And the read stayed a patch: it added no vault list walk.
		Expect(h.counter.get()).To(Equal(builds))
	})

	It("recomputes the session-started marker of a changed row", func() {
		h := newHarness(item("Alpha"))
		h.cache.started["Alpha"] = "2026-10-04T11:00:00Z"
		h.index.setPages(pageStatus("Alpha", "in_progress"))

		built, err := h.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(built[0].ClaudeSessionStarted).NotTo(BeNil())
		Expect(*built[0].ClaudeSessionStarted).To(Equal("2026-10-04T11:00:00Z"))
		builds := h.counter.get()

		// ClearTaskSession clears the marker and marks the page; the status cache
		// drops it in the same write. The patch must recompute the marker rather
		// than carry the held value over, which would republish the cleared one.
		delete(h.cache.started, "Alpha")
		h.index.markPages("Alpha.md")

		patched, err := h.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(patched[0].ClaudeSessionStarted).To(BeNil())
		Expect(h.counter.get()).To(Equal(builds))
	})
})
