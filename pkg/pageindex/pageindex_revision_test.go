// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/pageindex"
)

var _ = Describe("Revision", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("reports 0 for a key the index has never seen", func() {
		fake := newStorageFake()
		index := newIndex(fake)

		Expect(index.Revision(pageindex.NewKey("/vault-a", "24 Tasks"))).To(Equal(uint64(0)))
	})

	It("advances when a cold build publishes a snapshot", func() {
		key := pageindex.NewKey("/vault-a", "24 Tasks")
		fake := newStorageFake()
		fake.setPages(key, "one", "two")
		index := newIndex(fake)

		Expect(index.Revision(key)).To(Equal(uint64(0)))
		_, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
		Expect(err).To(BeNil())
		Expect(index.Revision(key)).To(Equal(uint64(1)))

		// A warm read publishes nothing and leaves the revision alone.
		_, err = index.ListPages(ctx, "/vault-a", "24 Tasks")
		Expect(err).To(BeNil())
		Expect(index.Revision(key)).To(Equal(uint64(1)))
	})

	It("advances on a folder-level mark", func() {
		key := pageindex.NewKey("/vault-a", "24 Tasks")
		fake := newStorageFake()
		fake.setPages(key, "one")
		index := newIndex(fake)

		_, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
		Expect(err).To(BeNil())
		before := index.Revision(key)

		index.MarkDirty(key)
		Expect(index.Revision(key)).To(BeNumerically(">", before))

		// An unknown key is still ignored, revision included.
		index.MarkDirty(pageindex.NewKey("/vault-z", "24 Tasks"))
		Expect(index.Revision(pageindex.NewKey("/vault-z", "24 Tasks"))).To(Equal(uint64(0)))
	})

	It("advances on a file-level mark and on the folder fallback", func() {
		key := pageindex.NewKey("/vault-a", "24 Tasks")
		fake := newStorageFake()
		fake.setPages(key, "one", "two")
		index := newIndex(fake)

		_, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
		Expect(err).To(BeNil())
		before := index.Revision(key)

		// The fake key has no on-disk file, so the exactness rule fails and the
		// mark widens to the folder — the fallback path.
		index.MarkFileDirty(key, "one")
		Expect(index.Revision(key)).To(BeNumerically(">", before))
	})

	It("advances on a forced reload", func() {
		key := pageindex.NewKey("/vault-a", "24 Tasks")
		fake := newStorageFake()
		fake.setPages(key, "one")
		index := newIndex(fake)

		_, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
		Expect(err).To(BeNil())
		before := index.Revision(key)

		index.ForceReload()
		Expect(index.Revision(key)).To(BeNumerically(">", before))
	})

	It("advances on a file-level mark when the file exists on disk", func() {
		vaultDir := newStatDiffVault()
		ri := newRealIndex()
		key := pageindex.NewKey(vaultDir, equivalenceFolder)

		pages, err := ri.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(BeNil())
		Expect(indexedNames(pages)).To(Equal([]string{"Alpha", "Beta"}))
		before := ri.index.Revision(key)

		ri.index.MarkFileDirty(key, "Alpha")
		Expect(ri.index.Revision(key)).To(BeNumerically(">", before))
	})

	It("advances when a rescan publishes a changed snapshot", func() {
		vaultDir := newStatDiffVault()
		ri := newRealIndex()
		key := pageindex.NewKey(vaultDir, equivalenceFolder)

		_, err := ri.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(BeNil())
		before := ri.index.Revision(key)

		settle()
		writeFixtureFile(vaultDir, "Alpha.md", "---\ntitle: Alpha\n---\n# Alpha changed\n")
		Expect(ri.index.Refresh(ctx, key)).To(BeNil())
		Expect(ri.index.Revision(key)).To(BeNumerically(">", before))
	})

	It("advances when a file event publishes a new page", func() {
		vaultDir := newStatDiffVault()
		ri := newRealIndex()
		key := pageindex.NewKey(vaultDir, equivalenceFolder)

		_, err := ri.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(BeNil())
		before := ri.index.Revision(key)

		writeFixtureFile(vaultDir, "Gamma.md", "---\ntitle: Gamma\n---\n# Gamma\n")
		Expect(ri.index.RefreshFile(ctx, key, "Gamma.md")).To(BeNil())
		Expect(ri.index.Revision(key)).To(BeNumerically(">", before))

		pages, err := ri.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(BeNil())
		Expect(indexedNames(pages)).To(Equal([]string{"Alpha", "Beta", "Gamma"}))
	})

	It("does not advance when a stat-diff finds nothing changed", func() {
		vaultDir := newStatDiffVault()
		ri := newRealIndex()
		key := pageindex.NewKey(vaultDir, equivalenceFolder)

		_, err := ri.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(BeNil())
		before := ri.index.Revision(key)
		ri.reader.reset()
		ri.lister.reset()

		Expect(ri.index.Refresh(ctx, key)).To(BeNil())
		Expect(ri.lister.Count()).To(Equal(1))
		Expect(ri.reader.Count()).To(Equal(0))
		Expect(ri.index.Revision(key)).To(Equal(before))
	})
})
