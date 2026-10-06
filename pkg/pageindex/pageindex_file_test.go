// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex_test

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/bborbe/vault-cli/pkg/domain"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/pageindex"
)

var _ = Describe("RefreshFile", func() {
	var (
		ctx   context.Context
		fake  *storageFake
		key   pageindex.Key
		index pageindex.PageIndex
	)

	// list reads the key and returns the published snapshot.
	list := func() []*domain.Page {
		pages, err := index.ListPages(ctx, key.VaultPath, key.PagesDir)
		Expect(err).To(BeNil())
		return pages
	}

	// readFile refreshes one file in a goroutine and reports the error.
	readFile := func(readCtx context.Context, filename string) <-chan error {
		done := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			done <- index.RefreshFile(readCtx, key, filename)
		}()
		return done
	}

	BeforeEach(func() {
		ctx = context.Background()
		fake = newStorageFake()
		key = pageindex.NewKey("/vault-a", "24 Tasks")
	})

	Describe("splicing one file into a new snapshot", func() {
		It("AC2(a) inserts a created file at its filename-ordered position", func() {
			fake.setPages(key, "alpha", "gamma")
			index = newIndex(fake)
			Expect(titles(list())).To(Equal([]string{"alpha", "gamma"}))

			fake.putPage(key, "beta", "# beta")
			Expect(index.RefreshFile(ctx, key, "beta.md")).To(Succeed())

			Expect(titles(list())).To(Equal([]string{"alpha", "beta", "gamma"}))
			// The folder is neither listed nor otherwise read.
			Expect(fake.lister.ListFilesCallCount()).To(Equal(1))
			Expect(fake.reader.ReadPageCallCount()).To(Equal(3))
		})

		It("AC2(b) replaces the changed page and shares every other pointer", func() {
			fake.setPages(key, "alpha", "beta", "gamma")
			index = newIndex(fake)
			before := list()
			Expect(titles(before)).To(Equal([]string{"alpha", "beta", "gamma"}))

			fake.putPage(key, "beta", "# beta changed")
			Expect(index.RefreshFile(ctx, key, "beta.md")).To(Succeed())

			after := list()
			Expect(titles(after)).To(Equal([]string{"alpha", "beta", "gamma"}))
			Expect(string(after[1].Content)).To(Equal("# beta changed"))
			// A splice, not a rebuild: the untouched pages are the same values.
			Expect(after[0]).To(BeIdenticalTo(before[0]))
			Expect(after[2]).To(BeIdenticalTo(before[2]))
			Expect(after[1]).NotTo(BeIdenticalTo(before[1]))
			Expect(fake.lister.ListFilesCallCount()).To(Equal(1))
		})

		It("AC2(c) removes a deleted file", func() {
			fake.setPages(key, "alpha", "beta")
			index = newIndex(fake)
			Expect(titles(list())).To(Equal([]string{"alpha", "beta"}))

			fake.dropPage(key, "beta")
			Expect(index.RefreshFile(ctx, key, "beta.md")).To(Succeed())

			Expect(titles(list())).To(Equal([]string{"alpha"}))
			Expect(fake.lister.ListFilesCallCount()).To(Equal(1))
		})

		It("AC2(d) removes a page whose file is gone, whatever the caller meant", func() {
			fake.setPages(key, "alpha", "beta")
			index = newIndex(fake)
			list()

			// A "modified" event for a file that no longer exists on disk: the
			// file's current state decides, not the event type.
			fake.dropPage(key, "beta")
			Expect(index.RefreshFile(ctx, key, "beta.md")).To(Succeed())

			Expect(titles(list())).To(Equal([]string{"alpha"}))
		})

		It("AC2(e) keeps a page whose file is still there", func() {
			fake.setPages(key, "alpha", "beta")
			index = newIndex(fake)
			list()

			// A "deleted" event for a file that still exists on disk.
			fake.putPage(key, "beta", "# beta still here")
			Expect(index.RefreshFile(ctx, key, "beta.md")).To(Succeed())

			pages := list()
			Expect(titles(pages)).To(Equal([]string{"alpha", "beta"}))
			Expect(string(pages[1].Content)).To(Equal("# beta still here"))
		})

		It("AC2(f) leaves a previously obtained slice and its pointers untouched", func() {
			fake.setPages(key, "alpha", "beta")
			index = newIndex(fake)
			before := list()
			captured := append([]*domain.Page(nil), before...)

			fake.putPage(key, "beta", "# beta changed")
			Expect(index.RefreshFile(ctx, key, "beta.md")).To(Succeed())

			Expect(before).To(HaveLen(2))
			Expect(before[0]).To(BeIdenticalTo(captured[0]))
			Expect(before[1]).To(BeIdenticalTo(captured[1]))
			Expect(string(before[1].Content)).To(Equal("# beta"))
		})

		It("rejects a filename that is not a single plain .md base name", func() {
			index = newIndex(fake)
			for _, filename := range []string{"", "a/b.md", "../x.md", "x.txt"} {
				Expect(index.RefreshFile(ctx, key, filename)).To(HaveOccurred(), filename)
			}
			Expect(fake.reader.ReadPageCallCount()).To(Equal(0))
			Expect(fake.lister.ListFilesCallCount()).To(Equal(0))
		})

		It("builds the folder when the key has no snapshot yet", func() {
			fake.setPages(key, "alpha", "beta")
			index = newIndex(fake)

			Expect(index.RefreshFile(ctx, key, "beta.md")).To(Succeed())

			Expect(titles(list())).To(Equal([]string{"alpha", "beta"}))
			Expect(fake.lister.ListFilesCallCount()).To(Equal(1))
			Expect(fake.reader.ReadPageCallCount()).To(Equal(2))
		})

		It("keeps a dirty mark that an unrelated RefreshFile does not clear", func() {
			fake.setPages(key, "alpha", "beta")
			index = newIndex(fake)
			list()

			fake.putPage(key, "gamma", "# gamma")
			index.MarkDirty(key)

			Expect(index.RefreshFile(ctx, key, "alpha.md")).To(Succeed())

			// The mark still forces the folder build, so the write is visible.
			Expect(titles(list())).To(Equal([]string{"alpha", "beta", "gamma"}))
			Expect(fake.lister.ListFilesCallCount()).To(Equal(2))
		})

		It("counts reads by reason", func() {
			buildBefore := pageindex.FilesReadTotal("build")
			eventBefore := pageindex.FilesReadTotal("event")

			fake.setPages(key, "alpha", "beta", "gamma")
			index = newIndex(fake)
			list()
			Expect(pageindex.FilesReadTotal("build") - buildBefore).To(Equal(3.0))

			fake.putPage(key, "beta", "# beta v2")
			Expect(index.RefreshFile(ctx, key, "beta.md")).To(Succeed())
			Expect(pageindex.FilesReadTotal("event") - eventBefore).To(Equal(1.0))
		})
	})

	Describe("ordering concurrent reads", func() {
		It("AC6(a) publishes the later-started read of a file", func() {
			fake.setPages(key, "alpha", "beta")
			index = newIndex(fake)
			list()

			entered, release := fake.blockReads("beta.md")
			first := readFile(ctx, "beta.md")
			Eventually(entered).Should(Receive(Equal("beta.md")))

			// The second read starts later and captures the new content.
			fake.putPage(key, "beta", "# beta v2")
			second := readFile(ctx, "beta.md")
			Eventually(entered).Should(Receive(Equal("beta.md")))

			// Release them so the earlier-started read finishes last.
			release("beta.md", 1)
			Expect(<-second).To(BeNil())
			release("beta.md", 0)
			Expect(<-first).To(BeNil())

			pages := list()
			Expect(string(pages[1].Content)).To(Equal("# beta v2"))
		})

		It("AC6(b) never drops one file's read for another's", func() {
			fake.setPages(key, "alpha", "beta")
			index = newIndex(fake)
			list()

			entered, release := fake.blockReads("alpha.md", "beta.md")

			fake.putPage(key, "alpha", "# alpha v2")
			alpha := readFile(ctx, "alpha.md")
			Eventually(entered).Should(Receive(Equal("alpha.md")))

			fake.putPage(key, "beta", "# beta v2")
			beta := readFile(ctx, "beta.md")
			Eventually(entered).Should(Receive(Equal("beta.md")))

			// Either release order keeps both results.
			release("beta.md", 0)
			Expect(<-beta).To(BeNil())
			release("alpha.md", 0)
			Expect(<-alpha).To(BeNil())

			pages := list()
			Expect(titles(pages)).To(Equal([]string{"alpha", "beta"}))
			Expect(string(pages[0].Content)).To(Equal("# alpha v2"))
			Expect(string(pages[1].Content)).To(Equal("# beta v2"))
		})

		It("AC6(b) keeps a file created while a folder build was listing", func() {
			fake.setPages(key, "alpha")
			index = newIndex(fake)
			list()

			entered, release := fake.block()
			fake.putPage(key, "alpha", "# alpha v2")
			buildDone := make(chan []*domain.Page, 1)
			go func() {
				defer GinkgoRecover()
				index.MarkDirty(key)
				pages, err := index.ListPages(ctx, key.VaultPath, key.PagesDir)
				Expect(err).To(BeNil())
				buildDone <- pages
			}()
			Eventually(entered).Should(Receive())

			// The event creates a file the build's listing never saw.
			fake.putPage(key, "beta", "# beta")
			Expect(index.RefreshFile(ctx, key, "beta.md")).To(Succeed())

			release()
			Expect(titles(<-buildDone)).To(Equal([]string{"alpha", "beta"}))
			Expect(titles(list())).To(Equal([]string{"alpha", "beta"}))
		})

		It("AC6(c) serves the previous snapshot while a read is blocked", func() {
			fake.setPages(key, "alpha", "beta")
			index = newIndex(fake)
			before := list()

			entered, release := fake.blockReads("beta.md")
			done := readFile(ctx, "beta.md")
			Eventually(entered).Should(Receive(Equal("beta.md")))

			start := time.Now()
			pages := list()
			Expect(time.Since(start)).To(BeNumerically("<", 100*time.Millisecond))
			Expect(pages[0]).To(BeIdenticalTo(before[0]))
			Expect(pages[1]).To(BeIdenticalTo(before[1]))

			release("beta.md", 0)
			Expect(<-done).To(BeNil())
		})

		It("AC6/behavior 6 lets an event beat a folder build that started earlier", func() {
			fake.setPages(key, "alpha", "beta")
			index = newIndex(fake)
			list()

			entered, release := fake.blockReads("alpha.md")
			index.MarkDirty(key)
			buildDone := make(chan []*domain.Page, 1)
			go func() {
				defer GinkgoRecover()
				pages, err := index.ListPages(ctx, key.VaultPath, key.PagesDir)
				Expect(err).To(BeNil())
				buildDone <- pages
			}()
			// The folder build lists, then blocks reading alpha.md.
			Eventually(entered).Should(Receive(Equal("alpha.md")))

			fake.putPage(key, "alpha", "# alpha v2")
			event := readFile(ctx, "alpha.md")
			Eventually(entered).Should(Receive(Equal("alpha.md")))
			release("alpha.md", 1)
			Expect(<-event).To(BeNil())

			// The build's older read is discarded, so even the caller that
			// joined the build sees the event's page.
			release("alpha.md", 0)
			Expect(string((<-buildDone)[0].Content)).To(Equal("# alpha v2"))
			Expect(string(list()[0].Content)).To(Equal("# alpha v2"))
		})

		It("AC6/behavior 6 lets a folder build beat an event that started earlier", func() {
			fake.setPages(key, "alpha", "beta")
			index = newIndex(fake)
			list()

			entered, release := fake.blockReads("alpha.md")
			event := readFile(ctx, "alpha.md")
			Eventually(entered).Should(Receive(Equal("alpha.md")))

			fake.putPage(key, "alpha", "# alpha v2")
			index.MarkDirty(key)
			buildDone := make(chan []*domain.Page, 1)
			go func() {
				defer GinkgoRecover()
				pages, err := index.ListPages(ctx, key.VaultPath, key.PagesDir)
				Expect(err).To(BeNil())
				buildDone <- pages
			}()
			Eventually(entered).Should(Receive(Equal("alpha.md")))

			release("alpha.md", 1)
			Expect(string((<-buildDone)[0].Content)).To(Equal("# alpha v2"))

			// The event's older read is discarded.
			release("alpha.md", 0)
			Expect(<-event).To(BeNil())
			Expect(string(list()[0].Content)).To(Equal("# alpha v2"))
		})
	})

	Describe("a cancelled caller", func() {
		It("still applies the read and reports the cancellation", func() {
			fake.setPages(key, "alpha", "beta")
			index = newIndex(fake)
			list()

			entered, release := fake.blockReads("alpha.md")
			fake.putPage(key, "alpha", "# alpha v2")

			readCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			done := readFile(readCtx, "alpha.md")
			Eventually(entered).Should(Receive(Equal("alpha.md")))

			cancel()
			release("alpha.md", 0)

			err := <-done
			Expect(err).To(HaveOccurred())
			Expect(stderrors.Is(err, context.Canceled)).To(BeTrue())
			// The read is applied, not dropped.
			Expect(string(list()[0].Content)).To(Equal("# alpha v2"))
		})
	})
})
