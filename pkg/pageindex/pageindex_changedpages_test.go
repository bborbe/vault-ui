// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/pageindex"
)

var _ = Describe("ChangedPagesSince", func() {
	var (
		ctx      context.Context
		fake     *storageFake
		vaultDir string
		key      pageindex.Key
	)

	BeforeEach(func() {
		ctx = context.Background()
		fake = newStorageFake()
		vaultDir = GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(vaultDir, "24 Tasks"), 0750)).To(Succeed())
		key = pageindex.NewKey(vaultDir, "24 Tasks")
	})

	// build publishes a key holding Alpha and Beta and returns the index with
	// the revision that publication left, which is the revision a derived
	// snapshot would have recorded.
	build := func() (pageindex.PageIndex, uint64) {
		fake.setPages(key, "Alpha", "Beta")
		for _, name := range []string{"Alpha", "Beta"} {
			Expect(os.WriteFile(pageFilePath(key, name), []byte("x"), 0600)).To(Succeed())
		}
		index := newIndex(fake)
		_, err := index.ListPages(ctx, key.VaultPath, key.PagesDir)
		Expect(err).To(BeNil())
		return index, index.Revision(key)
	}

	DescribeTable("bounds the set of pages that moved",
		func(mutate func(index pageindex.PageIndex), expected []string, ok bool) {
			index, revision := build()
			mutate(index)

			names, got := index.ChangedPagesSince(key, revision)
			Expect(got).To(Equal(ok))
			if !ok {
				Expect(names).To(BeEmpty())
				return
			}
			elements := make([]any, 0, len(expected))
			for _, name := range expected {
				elements = append(elements, name)
			}
			Expect(names).To(ConsistOf(elements...))
		},
		Entry("a per-file write mark reports exactly that page with its .md suffix",
			func(index pageindex.PageIndex) { index.MarkFileDirty(key, "Alpha") },
			[]string{"Alpha.md"}, true),
		Entry("a per-file write mark given as a wikilink reports the same name",
			func(index pageindex.PageIndex) { index.MarkFileDirty(key, "[[Alpha]]") },
			[]string{"Alpha.md"}, true),
		Entry("two per-file marks report both pages",
			func(index pageindex.PageIndex) {
				index.MarkFileDirty(key, "Alpha")
				index.MarkFileDirty(key, "Beta")
			},
			[]string{"Alpha.md", "Beta.md"}, true),
		Entry("a single-file re-read that changed the page reports it",
			func(index pageindex.PageIndex) {
				fake.putPage(key, "Alpha", "# Alpha v2")
				Expect(index.RefreshFile(ctx, key, "Alpha.md")).To(Succeed())
			},
			[]string{"Alpha.md"}, true),
		Entry("a stat-diff that changed one page reports it",
			func(index pageindex.PageIndex) {
				fake.putPage(key, "Alpha", "# Alpha v2")
				Expect(index.Refresh(ctx, key)).To(Succeed())
			},
			[]string{"Alpha.md"}, true),
		Entry("a stat-diff that changed nothing reports nothing",
			func(index pageindex.PageIndex) {
				Expect(index.Refresh(ctx, key)).To(Succeed())
			},
			nil, true),
		Entry("an unchanged key reports nothing",
			func(pageindex.PageIndex) {},
			nil, true),
		Entry("a folder-level mark cannot be bounded",
			func(index pageindex.PageIndex) { index.MarkDirty(key) },
			nil, false),
		Entry("a forced reload cannot be bounded",
			func(index pageindex.PageIndex) { index.ForceReload() },
			nil, false),
		Entry("a revision the key has advanced past its retained window cannot be placed",
			func(index pageindex.PageIndex) {
				for i := 0; i < 70; i++ {
					index.MarkFileDirty(key, "Alpha")
				}
			},
			nil, false),
	)

	It("reports unknown for a key it has never seen", func() {
		index, _ := build()
		_, ok := index.ChangedPagesSince(pageindex.NewKey(vaultDir, "23 Goals"), 0)
		Expect(ok).To(BeFalse())
	})

	It("reports unknown for a revision the key has never reached", func() {
		index, revision := build()
		_, ok := index.ChangedPagesSince(key, revision+5)
		Expect(ok).To(BeFalse())
	})

	It("reports unknown once an unbounded mark falls inside the range", func() {
		index, revision := build()
		index.MarkFileDirty(key, "Alpha")
		index.MarkDirty(key)
		names, ok := index.ChangedPagesSince(key, revision)
		Expect(ok).To(BeFalse())
		Expect(names).To(BeEmpty())
	})

	It("reports unknown after a cold build publishes the whole snapshot", func() {
		// A key's first publication is the whole snapshot, so a caller holding the
		// revision from before it (0, the value an unknown key reports) cannot be
		// told a bounded set of rows moved.
		index, _ := build()
		names, ok := index.ChangedPagesSince(key, 0)
		Expect(ok).To(BeFalse())
		Expect(names).To(BeEmpty())
	})

	It("reports unknown after a hydrate publishes the whole snapshot", func() {
		// The store-backed hydrate is also a first publication, but it seeds the
		// stored pages as its stat-diff baseline, so its delta names only the files
		// it re-read and never the pages it carried forward. It must not claim a
		// bound the cold path refuses: both first publications are unbounded.
		storeVaultDir := newStoreVault(3)
		storeKey := pageindex.NewKey(storeVaultDir, equivalenceFolder)
		store := newHydrationStore(GinkgoT())
		populateStore(ctx, store, storeKey, storeVaultDir, equivalenceFolder)

		settle()
		writeFixtureFile(
			storeVaultDir,
			"Page01.md",
			"---\ntitle: Page01\n---\n# Page01 v2\n",
		)

		start := newStoreStart(store)
		pages, err := start.index.ListPages(ctx, storeVaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(pages).To(HaveLen(3))
		// The hydrate re-read exactly the one changed file, so the delta it would
		// have reported is a partial set — the under-reporting case, not an empty
		// one.
		Expect(start.reader.Names()).To(Equal([]string{"Page01.md"}))

		names, ok := start.index.ChangedPagesSince(storeKey, 0)
		Expect(ok).To(BeFalse())
		Expect(names).To(BeEmpty())
	})
})
