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

	"github.com/bborbe/vault-ui/pkg/board"
)

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
})
