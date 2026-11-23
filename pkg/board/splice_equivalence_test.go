// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board_test

import (
	"context"
	"sync"

	"github.com/bborbe/errors"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/ops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/board"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	pageindexmocks "github.com/bborbe/vault-ui/pkg/pageindex/mocks"
)

// mutablePages is a storage.PageStorage whose page set can be swapped between
// reads, so a test can drive the board's Ops provider and its page index from
// one page set and then move that set as a write would.
type mutablePages struct {
	mu    sync.Mutex
	pages []*domain.Page
}

func (m *mutablePages) set(pages []*domain.Page) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pages = pages
}

func (m *mutablePages) ListPages(
	_ context.Context, _, _ string,
) ([]*domain.Page, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*domain.Page(nil), m.pages...), nil
}

func (m *mutablePages) ReadPage(
	ctx context.Context, _, _, name string,
) (*domain.Page, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, page := range m.pages {
		if page.FileMetadata.Name == name {
			return page, nil
		}
	}
	return nil, errors.Errorf(ctx, "page %q is not in the fixture", name)
}

// pageOps is an OpsProvider whose list operation runs vault-cli's real list
// operation over a mutable page set, so the full build and the patch path's
// new-page splice both derive their items from the same pages.
type pageOps struct {
	storage *mutablePages
}

func (o pageOps) List(_ board.Vault) ops.ListOperation {
	return ops.NewListOperation(o.storage)
}

func (o pageOps) TopicShow(_ board.Vault) ops.EntityShowOperation {
	return fakeShow{}
}

// spliceHarness wires a board whose Ops provider and page index share one
// mutable page set, so a page added between two reads is visible to both the
// full build (Ops) and the patch (index ListEntries + changed set).
type spliceHarness struct {
	board   board.Board
	storage *mutablePages
	index   *fakeIndex
	probe   *fakeProbe
}

func newSpliceHarness(pages ...*domain.Page) *spliceHarness {
	storage := &mutablePages{pages: pages}
	index := &fakeIndex{}
	index.setPages(pages...)
	probe := &fakeProbe{}
	// Production wires one page index to both the Ops provider and the board's
	// page-fields seam; backing the mock with the same pages keeps the two sides
	// deriving jira/mode/Open Questions from one source, so a divergence is
	// caught rather than masked by an empty listing.
	pageIndex := &pageindexmocks.PageIndex{}
	pageIndex.ListEntriesReturns(asEntries(pages), nil)
	h := &spliceHarness{storage: storage, index: index, probe: probe}
	h.board = board.New(board.Deps{
		Vaults: fakeVaults{vaults: []board.Vault{vault}},
		Ops:    pageOps{storage: storage},
		Cache:  fakeCache{statuses: map[string]string{}, started: map[string]string{}},
		Launch: launchregistry.NewRegistry(),
		Clock: libtime.CurrentDateTimeGetterFunc(
			func() libtime.DateTime { return libtime.DateTime(baseTime) },
		),
		Signals:   fakeSignals{},
		Sessions:  probe,
		Index:     index,
		PageIndex: pageIndex,
		Source:    sourceFromPages(pages),
	})
	return h
}

// responsesByID indexes a response set by task id so two sets compare
// independently of the order the board served them in.
func responsesByID(responses []api.TaskResponse) map[string]api.TaskResponse {
	byID := make(map[string]api.TaskResponse, len(responses))
	for _, response := range responses {
		byID[response.ID] = response
	}
	return byID
}

var _ = Describe("bounded rebuild equivalence", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("splices a new page's row so the result equals a full rebuild", func() {
		// The patch path: build from a one-page vault, then add a second page —
		// carrying jira, mode and Open Questions, so the page-derived fields the
		// splice must reproduce are actually exercised — and mark it as new.
		patched := newSpliceHarness(pageStatus("Alpha", "todo"))
		_, err := patched.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())

		newPage := pageWithFrontmatter(
			"New",
			map[string]any{"status": "in_progress", "jira": "BRO-1", "mode": "headless"},
			"## Open Questions\n\n- Which vault?\n",
		)
		pagesAfterWrite := []*domain.Page{pageStatus("Alpha", "todo"), newPage}
		patched.storage.set(pagesAfterWrite)
		patched.index.setPages(pagesAfterWrite...)
		patched.index.markPages("New.md")

		patchedRows, err := patched.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(patchedRows).To(HaveLen(2))
		Expect(taskByID(patchedRows, "New").Jira).NotTo(BeNil())
		Expect(*taskByID(patchedRows, "New").Jira).To(Equal("BRO-1"))

		// The full-build path: a cold board over the same two pages.
		full := newSpliceHarness(pagesAfterWrite...)
		fullRows, err := full.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(fullRows).To(HaveLen(2))

		Expect(responsesByID(patchedRows)).To(Equal(responsesByID(fullRows)))
	})

	It("patches and refreshes a both-moved key so the result equals a full rebuild", func() {
		// Seed a one-page vault and read it, so a snapshot exists to patch.
		bothMoved := newSpliceHarness(pageStatus("Alpha", "todo"))
		_, err := bothMoved.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())

		// A page write and a session-snapshot refresh land in the same window.
		pagesAfterWrite := []*domain.Page{pageStatus("Alpha", "in_progress")}
		bothMoved.storage.set(pagesAfterWrite)
		bothMoved.index.setPages(pagesAfterWrite...)
		bothMoved.index.markPages("Alpha.md")

		// Advance the session generation the board reads through its Sessions dep.
		bothMoved.probe.bumpGeneration()

		patchedRows, err := bothMoved.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(patchedRows).To(HaveLen(1))

		// The full-build path: a cold board over the same page and generation.
		full := newSpliceHarness(pagesAfterWrite...)
		full.probe.bumpGeneration()
		fullRows, err := full.board.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(fullRows).To(HaveLen(1))

		Expect(responsesByID(patchedRows)).To(Equal(responsesByID(fullRows)))
	})
})
