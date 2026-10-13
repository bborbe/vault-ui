// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board

import (
	"context"
	stderrors "errors"
	"fmt"
	"sync"
	"time"

	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/ops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/pageindex"
)

// fakeRevisions is a mutable IndexRevisions.
type fakeRevisions struct {
	mu    sync.Mutex
	value uint64
	// changed is the set ChangedPagesSince reports and changedOK whether it can
	// bound it. A test sets them to simulate a per-file mark or a folder-level
	// one.
	changed   []string
	changedOK bool
	// pageReads counts ListPages calls. The store never makes one — the page
	// read belongs to the board's patch func — so a non-zero count is a bug.
	pageReads int
}

func (f *fakeRevisions) Revision(pageindex.Key) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.value
}

func (f *fakeRevisions) ChangedPagesSince(_ pageindex.Key, revision uint64) ([]string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if revision > f.value {
		return nil, false
	}
	return append([]string(nil), f.changed...), f.changedOK
}

func (f *fakeRevisions) ListPages(context.Context, string, string) ([]*domain.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pageReads++
	return nil, nil
}

func (f *fakeRevisions) bump() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.value++
}

// markPages advances the revision and reports the given pages as changed.
func (f *fakeRevisions) markPages(names ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.value++
	f.changed = append([]string(nil), names...)
	f.changedOK = true
}

// markFolder advances the revision without bounding the changed set.
func (f *fakeRevisions) markFolder() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.value++
	f.changed = nil
	f.changedOK = false
}

// PageReads reports how many ListPages calls the store made.
func (f *fakeRevisions) PageReads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pageReads
}

// fakeGenerations is a mutable generationSource.
type fakeGenerations struct {
	mu    sync.Mutex
	value uint64
}

func (f *fakeGenerations) Generation() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.value
}

func (f *fakeGenerations) bump() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.value++
}

// snapshotHarness wires the store under test to the two fakes and counts the
// builds it runs. The build callback's third argument is the build's ordinal.
type snapshotHarness struct {
	store       *taskSnapshotStore
	revisions   *fakeRevisions
	generations *fakeGenerations

	mu         sync.Mutex
	builds     int
	patches    int
	refreshes  int
	patchNames []string
}

// snapshotHooks carries the store's optional cheaper rebuild funcs. A nil hook
// leaves that path off, which is the "always rebuild in full" configuration for
// it.
type snapshotHooks struct {
	patch   taskSnapshotPatchFunc
	refresh taskSnapshotRefreshFunc
}

func newSnapshotHarness(
	build func(ctx context.Context, vault Vault, n int) ([]taskSnapshotRow, error),
	timeout time.Duration,
) *snapshotHarness {
	return newSnapshotHarnessWithHooks(build, snapshotHooks{}, timeout)
}

// newSnapshotHarnessWithHooks is newSnapshotHarness with the cheaper rebuild
// paths wired, so a test can drive them.
func newSnapshotHarnessWithHooks(
	build func(ctx context.Context, vault Vault, n int) ([]taskSnapshotRow, error),
	hooks snapshotHooks,
	timeout time.Duration,
) *snapshotHarness {
	h := &snapshotHarness{
		revisions:   &fakeRevisions{},
		generations: &fakeGenerations{},
	}
	params := taskSnapshotParams{
		Build: func(ctx context.Context, vault Vault) ([]taskSnapshotRow, error) {
			h.mu.Lock()
			h.builds++
			n := h.builds
			h.mu.Unlock()
			return build(ctx, vault, n)
		},
		Revisions:   h.revisions,
		Generations: h.generations,
		Timeout:     timeout,
	}
	if hooks.patch != nil {
		params.Patch = func(
			ctx context.Context, vault Vault, rows []taskSnapshotRow, names []string,
		) ([]taskSnapshotRow, error) {
			h.mu.Lock()
			h.patches++
			h.patchNames = append([]string(nil), names...)
			h.mu.Unlock()
			return hooks.patch(ctx, vault, rows, names)
		}
	}
	if hooks.refresh != nil {
		params.Refresh = func(
			ctx context.Context, vault Vault, rows []taskSnapshotRow,
		) ([]taskSnapshotRow, error) {
			h.mu.Lock()
			h.refreshes++
			h.mu.Unlock()
			return hooks.refresh(ctx, vault, rows)
		}
	}
	h.store = newTaskSnapshotStore(params)
	return h
}

func (h *snapshotHarness) Builds() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.builds
}

// Patches reports how many row patches ran, and the changed set the last one was
// given.
func (h *snapshotHarness) Patches() (int, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.patches, append([]string(nil), h.patchNames...)
}

// Refreshes reports how many session-only refreshes ran.
func (h *snapshotHarness) Refreshes() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.refreshes
}

// snapshotRow builds a precomputed row carrying just a name.
func snapshotRow(name string) taskSnapshotRow {
	return taskSnapshotRow{item: ops.TaskListItem{Name: name}}
}

// numberedRows returns count rows all carrying the same marker, so a reader can
// tell one published set from a mixture of two.
func numberedRows(marker string, count int) []taskSnapshotRow {
	rows := make([]taskSnapshotRow, 0, count)
	for i := 0; i < count; i++ {
		rows = append(rows, snapshotRow(marker))
	}
	return rows
}

var snapshotVaultA = Vault{Name: "vault-a", Path: "/vault-a", TasksFolder: "24 Tasks"}
var snapshotVaultB = Vault{Name: "vault-b", Path: "/vault-b", TasksFolder: "24 Tasks"}

var _ = Describe("taskSnapshotStore", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("builds once and serves the identical slice on a warm read", func() {
		h := newSnapshotHarness(
			func(_ context.Context, vault Vault, n int) ([]taskSnapshotRow, error) {
				return numberedRows(fmt.Sprintf("%s-%d", vault.Name, n), 1), nil
			},
			0,
		)

		first, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(first).To(HaveLen(1))
		Expect(first[0].item.Name).To(Equal("vault-a-1"))
		Expect(h.Builds()).To(Equal(1))

		second, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(second).To(HaveLen(1))
		Expect(&second[0]).To(BeIdenticalTo(&first[0]))
		Expect(h.Builds()).To(Equal(1))
	})

	It("AC6 shares one build across concurrent cold reads", func() {
		entered := make(chan struct{}, 8)
		release := make(chan struct{})
		h := newSnapshotHarness(
			func(_ context.Context, _ Vault, _ int) ([]taskSnapshotRow, error) {
				entered <- struct{}{}
				<-release
				return numberedRows("shared", 1), nil
			},
			0,
		)

		results := make([][]taskSnapshotRow, 2)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				defer GinkgoRecover()
				results[i], errs[i] = h.store.List(ctx, snapshotVaultA)
			}(i)
		}

		Eventually(entered).Should(Receive())
		Consistently(h.Builds, "100ms").Should(Equal(1))
		close(release)
		wg.Wait()

		Expect(h.Builds()).To(Equal(1))
		for i := range results {
			Expect(errs[i]).To(BeNil())
			Expect(results[i]).To(HaveLen(1))
		}
		Expect(&results[0][0]).To(BeIdenticalTo(&results[1][0]))
	})

	It("rebuilds when the page-index revision moves", func() {
		h := newSnapshotHarness(
			func(_ context.Context, _ Vault, n int) ([]taskSnapshotRow, error) {
				return numberedRows(fmt.Sprintf("row-%d", n), 1), nil
			},
			0,
		)

		first, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(first[0].item.Name).To(Equal("row-1"))

		h.revisions.bump()
		second, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(second[0].item.Name).To(Equal("row-2"))
		Expect(h.Builds()).To(Equal(2))
	})

	It("rebuilds when the session generation moves", func() {
		h := newSnapshotHarness(
			func(_ context.Context, _ Vault, n int) ([]taskSnapshotRow, error) {
				return numberedRows(fmt.Sprintf("row-%d", n), 1), nil
			},
			0,
		)

		_, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(h.Builds()).To(Equal(1))

		h.generations.bump()
		second, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(second[0].item.Name).To(Equal("row-2"))
		Expect(h.Builds()).To(Equal(2))
	})

	It("keeps the previous rows when a rebuild fails and retries on the next read", func() {
		h := newSnapshotHarness(
			func(_ context.Context, _ Vault, n int) ([]taskSnapshotRow, error) {
				if n == 2 {
					return nil, stderrors.New("boom")
				}
				return numberedRows(fmt.Sprintf("row-%d", n), 1), nil
			},
			0,
		)

		first, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(first[0].item.Name).To(Equal("row-1"))

		h.revisions.bump()
		retained, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(retained[0].item.Name).To(Equal("row-1"))
		Expect(h.Builds()).To(Equal(2))

		retried, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(retried[0].item.Name).To(Equal("row-3"))
		Expect(h.Builds()).To(Equal(3))
	})

	It("advances the snapshot generation on a successful rebuild only", func() {
		h := newSnapshotHarness(
			func(_ context.Context, _ Vault, n int) ([]taskSnapshotRow, error) {
				if n == 2 {
					return nil, stderrors.New("boom")
				}
				return numberedRows(fmt.Sprintf("row-%d", n), 1), nil
			},
			0,
		)

		_, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(h.store.SnapshotGeneration()).To(Equal(uint64(1)))

		h.revisions.bump()
		_, err = h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(h.store.SnapshotGeneration()).To(Equal(uint64(1)))

		_, err = h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(h.store.SnapshotGeneration()).To(Equal(uint64(2)))
	})

	It("returns the error of a failed cold build and retries the next read", func() {
		h := newSnapshotHarness(
			func(_ context.Context, _ Vault, _ int) ([]taskSnapshotRow, error) {
				return nil, stderrors.New("boom")
			},
			0,
		)

		_, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("build task snapshot for vault vault-a"))

		_, err = h.store.List(ctx, snapshotVaultA)
		Expect(err).To(HaveOccurred())
		Expect(h.Builds()).To(Equal(2))
	})

	It("does not clear an invalidation recorded while a build was in flight", func() {
		var h *snapshotHarness
		h = newSnapshotHarness(
			func(_ context.Context, _ Vault, n int) ([]taskSnapshotRow, error) {
				if n == 1 {
					h.revisions.bump()
				}
				return numberedRows(fmt.Sprintf("row-%d", n), 1), nil
			},
			0,
		)

		first, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(first[0].item.Name).To(Equal("row-1"))

		second, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(second[0].item.Name).To(Equal("row-2"))
		Expect(h.Builds()).To(Equal(2))
	})

	It("never mixes two published row sets under a concurrent reader", func() {
		h := newSnapshotHarness(
			func(_ context.Context, _ Vault, n int) ([]taskSnapshotRow, error) {
				return numberedRows(fmt.Sprintf("row-%d", n), 3), nil
			},
			0,
		)

		var mu sync.Mutex
		var observed [][]taskSnapshotRow
		var wg sync.WaitGroup
		stop := make(chan struct{})

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer GinkgoRecover()
			for {
				select {
				case <-stop:
					return
				default:
				}
				rows, err := h.store.List(ctx, snapshotVaultA)
				Expect(err).To(BeNil())
				mu.Lock()
				observed = append(observed, rows)
				mu.Unlock()
				time.Sleep(100 * time.Microsecond)
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				h.revisions.bump()
				time.Sleep(time.Millisecond)
			}
			close(stop)
		}()
		wg.Wait()

		mu.Lock()
		defer mu.Unlock()
		Expect(observed).NotTo(BeEmpty())
		for _, rows := range observed {
			Expect(rows).To(HaveLen(3))
			for _, row := range rows {
				Expect(row.item.Name).To(Equal(rows[0].item.Name))
			}
		}
	})

	It("bounds a hung build by the timeout", func() {
		h := newSnapshotHarness(
			func(ctx context.Context, _ Vault, _ int) ([]taskSnapshotRow, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
			20*time.Millisecond,
		)

		done := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := h.store.List(ctx, snapshotVaultA)
			done <- err
		}()
		Eventually(done, "5s").Should(Receive(HaveOccurred()))
	})

	It("returns a wrapped error when a waiting caller's ctx is cancelled", func() {
		entered := make(chan struct{}, 1)
		release := make(chan struct{})
		h := newSnapshotHarness(
			func(_ context.Context, _ Vault, _ int) ([]taskSnapshotRow, error) {
				entered <- struct{}{}
				<-release
				return numberedRows("shared", 1), nil
			},
			0,
		)

		ownerDone := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(ownerDone)
			_, err := h.store.List(context.Background(), snapshotVaultA)
			Expect(err).To(BeNil())
		}()
		Eventually(entered).Should(Receive())

		waitCtx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := h.store.List(waitCtx, snapshotVaultA)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("wait for task snapshot build"))

		close(release)
		Eventually(ownerDone, "5s").Should(BeClosed())
	})

	It("keeps different keys independent", func() {
		entered := make(chan struct{}, 1)
		release := make(chan struct{})
		h := newSnapshotHarness(
			func(_ context.Context, vault Vault, _ int) ([]taskSnapshotRow, error) {
				if vault.Name == "vault-a" {
					entered <- struct{}{}
					<-release
				}
				return numberedRows(vault.Name, 1), nil
			},
			0,
		)

		blocked := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(blocked)
			_, err := h.store.List(ctx, snapshotVaultA)
			Expect(err).To(BeNil())
		}()
		Eventually(entered).Should(Receive())

		rows, err := h.store.List(ctx, snapshotVaultB)
		Expect(err).To(BeNil())
		Expect(rows[0].item.Name).To(Equal("vault-b"))
		Expect(h.Builds()).To(Equal(2))
		Consistently(blocked, "100ms").ShouldNot(BeClosed())

		close(release)
		Eventually(blocked, "5s").Should(BeClosed())
	})
})

// namedRows returns one row per name, each with an empty status, so a patch can
// be seen to change exactly one of them.
func namedRows(names ...string) []taskSnapshotRow {
	rows := make([]taskSnapshotRow, 0, len(names))
	for _, name := range names {
		rows = append(rows, snapshotRow(name))
	}
	return rows
}

// rowsBuilder is a Build func returning the given rows whatever the ordinal.
func rowsBuilder(names ...string) func(context.Context, Vault, int) ([]taskSnapshotRow, error) {
	return func(_ context.Context, _ Vault, _ int) ([]taskSnapshotRow, error) {
		return namedRows(names...), nil
	}
}

// statusPatch returns a patch func that sets one named row's status, leaving
// every other row as it was.
func statusPatch(name, status string) taskSnapshotPatchFunc {
	return func(
		_ context.Context, _ Vault, rows []taskSnapshotRow, _ []string,
	) ([]taskSnapshotRow, error) {
		patched := make([]taskSnapshotRow, len(rows))
		copy(patched, rows)
		for i := range patched {
			if patched[i].item.Name == name {
				patched[i].item.Status = status
			}
		}
		return patched, nil
	}
}

// allStatusRefresh returns a refresh func that marks every row as refreshed.
func allStatusRefresh(status string) taskSnapshotRefreshFunc {
	return func(_ context.Context, _ Vault, rows []taskSnapshotRow) ([]taskSnapshotRow, error) {
		refreshed := make([]taskSnapshotRow, len(rows))
		copy(refreshed, rows)
		for i := range refreshed {
			refreshed[i].item.Status = status
		}
		return refreshed, nil
	}
}

var _ = Describe("taskSnapshotStore row patch", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("re-derives only the changed rows and never rebuilds", func() {
		h := newSnapshotHarnessWithHooks(
			rowsBuilder("Alpha", "Beta"),
			snapshotHooks{patch: statusPatch("Alpha", "patched")},
			0,
		)

		first, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(first).To(HaveLen(2))
		Expect(h.Builds()).To(Equal(1))

		h.revisions.markPages("Alpha.md")
		second, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(second).To(HaveLen(2))
		Expect(second[0].item.Name).To(Equal("Alpha"))
		Expect(second[0].item.Status).To(Equal("patched"))
		Expect(second[1].item.Name).To(Equal("Beta"))
		Expect(second[1].item.Status).To(Equal(""))

		Expect(h.Builds()).To(Equal(1))
		patches, names := h.Patches()
		Expect(patches).To(Equal(1))
		Expect(names).To(Equal([]string{"Alpha.md"}))

		// The atomic swap: the slice the first read returned is untouched.
		Expect(first).To(HaveLen(2))
		Expect(first[0].item.Status).To(Equal(""))
		Expect(first[1].item.Status).To(Equal(""))

		// The store itself never reads a page: that belongs to the patch func.
		Expect(h.revisions.PageReads()).To(Equal(0))
	})

	It("rebuilds in full for a folder-level mark", func() {
		h := newSnapshotHarnessWithHooks(
			rowsBuilder("Alpha"),
			snapshotHooks{patch: statusPatch("Alpha", "patched")},
			0,
		)
		_, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())

		h.revisions.markFolder()
		rows, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(rows[0].item.Status).To(Equal(""))
		Expect(h.Builds()).To(Equal(2))
		patches, _ := h.Patches()
		Expect(patches).To(Equal(0))
	})

	It("rebuilds in full when no patch func is wired", func() {
		h := newSnapshotHarness(rowsBuilder("Alpha"), 0)
		_, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())

		h.revisions.markPages("Alpha.md")
		_, err = h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(h.Builds()).To(Equal(2))
	})

	It("takes the session-only refresh when only the generation moves", func() {
		h := newSnapshotHarnessWithHooks(
			rowsBuilder("Alpha"),
			snapshotHooks{
				patch:   statusPatch("Alpha", "patched"),
				refresh: allStatusRefresh("refreshed"),
			},
			0,
		)
		_, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())

		h.generations.bump()
		rows, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(rows[0].item.Status).To(Equal("refreshed"))
		Expect(h.Refreshes()).To(Equal(1))
		Expect(h.Builds()).To(Equal(1))
		patches, _ := h.Patches()
		Expect(patches).To(Equal(0))
	})

	It("rebuilds in full when the page revision and the generation both move", func() {
		h := newSnapshotHarnessWithHooks(
			rowsBuilder("Alpha"),
			snapshotHooks{
				patch:   statusPatch("Alpha", "patched"),
				refresh: allStatusRefresh("refreshed"),
			},
			0,
		)
		_, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())

		h.revisions.markPages("Alpha.md")
		h.generations.bump()
		rows, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		// A patch here would have published stale session fields, and a refresh
		// would have dropped the page move: neither is correct.
		Expect(rows[0].item.Status).To(Equal(""))
		Expect(h.Builds()).To(Equal(2))
		Expect(h.Refreshes()).To(Equal(0))
		patches, _ := h.Patches()
		Expect(patches).To(Equal(0))
	})

	It("keeps the previous rows when a patch fails and retries the next read", func() {
		h := newSnapshotHarnessWithHooks(
			rowsBuilder("Alpha"),
			snapshotHooks{patch: func(
				_ context.Context, _ Vault, _ []taskSnapshotRow, _ []string,
			) ([]taskSnapshotRow, error) {
				return nil, stderrors.New("boom")
			}},
			0,
		)
		first, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(first[0].item.Name).To(Equal("Alpha"))

		h.revisions.markPages("Alpha.md")
		retained, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(retained[0].item.Name).To(Equal("Alpha"))
		patches, _ := h.Patches()
		Expect(patches).To(Equal(1))

		retried, err := h.store.List(ctx, snapshotVaultA)
		Expect(err).To(BeNil())
		Expect(retried[0].item.Name).To(Equal("Alpha"))
		patches, _ = h.Patches()
		Expect(patches).To(Equal(2))
	})
})

var _ = Describe("changedPages", func() {
	It("serves only the changed pages it was given", func() {
		alpha := domain.NewPage(
			map[string]any{"status": "todo"},
			domain.FileMetadata{Name: "Alpha"},
			domain.Content("# Alpha"),
		)
		storage := &changedPages{pages: []*domain.Page{alpha}}

		pages, err := storage.ListPages(context.Background(), "/vault", "24 Tasks")
		Expect(err).To(BeNil())
		Expect(pages).To(Equal([]*domain.Page{alpha}))

		page, err := storage.ReadPage(context.Background(), "/vault", "24 Tasks", "Alpha")
		Expect(err).To(BeNil())
		Expect(page).To(Equal(alpha))

		_, err = storage.ReadPage(context.Background(), "/vault", "24 Tasks", "Missing")
		Expect(err).To(HaveOccurred())
	})
})
