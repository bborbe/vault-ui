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

var _ = Describe("ReadPage", func() {
	var (
		ctx  context.Context
		fake *storageFake
	)

	BeforeEach(func() {
		ctx = context.Background()
		fake = newStorageFake()
	})

	It("serves a page from the in-memory snapshot without touching storage", func() {
		key := pageindex.NewKey("/vault-a", "24 Tasks")
		fake.setPages(key, "one", "two")
		index := newIndex(fake)
		Expect(index.Build(ctx, []pageindex.Key{key})).To(BeNil())
		// The build itself reads every file through the seam; only the calls
		// after it are this method's.
		before := fake.reader.ReadPageCallCount()

		page, err := index.ReadPage(ctx, "/vault-a", "24 Tasks", "two")
		Expect(err).NotTo(HaveOccurred())
		Expect(page.FileMetadata.Name).To(Equal("two"))
		Expect(page.Content.String()).To(Equal("# two"))
		Expect(fake.reader.ReadPageCallCount()).To(Equal(before))
	})

	It("falls through to the reader seam for a page the snapshot does not hold", func() {
		key := pageindex.NewKey("/vault-a", "24 Tasks")
		fake.setPages(key, "one")
		index := newIndex(fake)
		Expect(index.Build(ctx, []pageindex.Key{key})).To(BeNil())

		// Written behind the index's back, so the snapshot cannot hold it and
		// only the seam can answer.
		fake.putPage(key, "later", "# Later")
		before := fake.reader.ReadPageCallCount()

		page, err := index.ReadPage(ctx, "/vault-a", "24 Tasks", "later")
		Expect(err).NotTo(HaveOccurred())
		Expect(page.FileMetadata.Name).To(Equal("later"))
		Expect(page.Content.String()).To(Equal("# Later"))

		// The seam's fourth argument is a filename including ".md", which is the
		// opposite of this method's bare base name.
		Expect(fake.reader.ReadPageCallCount()).To(Equal(before + 1))
		_, vaultPath, pagesDir, filename := fake.reader.ReadPageArgsForCall(before)
		Expect(vaultPath).To(Equal("/vault-a"))
		Expect(pagesDir).To(Equal("24 Tasks"))
		Expect(filename).To(Equal("later.md"))
	})

	It("returns the seam's error for a missing page instead of skipping it", func() {
		index := newIndex(fake)

		_, err := index.ReadPage(ctx, "/vault-a", "24 Tasks", "missing")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("page not found: missing"))
		Expect(fake.reader.ReadPageCallCount()).To(Equal(1))
	})

	DescribeTable("refuses a name that must never become a file path",
		func(name string) {
			index := newIndex(fake)

			_, err := index.ReadPage(ctx, "/vault-a", "24 Tasks", name)
			Expect(err).To(HaveOccurred())
			Expect(fake.reader.ReadPageCallCount()).To(Equal(0))
		},
		Entry("empty", ""),
		Entry("path separator", "sub/Page"),
		Entry("parent directory", ".."),
	)
})
