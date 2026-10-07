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

	"github.com/bborbe/vault-cli/pkg/ops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/pageindex"
)

// fakeRevisions is a mutable IndexRevisions.
type fakeRevisions struct {
	mu    sync.Mutex
	value uint64
}

func (f *fakeRevisions) Revision(pageindex.Key) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.value
}

func (f *fakeRevisions) bump() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.value++
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

	mu     sync.Mutex
	builds int
}

func newSnapshotHarness(
	build func(ctx context.Context, vault Vault, n int) ([]taskSnapshotRow, error),
	timeout time.Duration,
) *snapshotHarness {
	h := &snapshotHarness{
		revisions:   &fakeRevisions{},
		generations: &fakeGenerations{},
	}
	h.store = newTaskSnapshotStore(taskSnapshotParams{
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
	})
	return h
}

func (h *snapshotHarness) Builds() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.builds
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
