// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/pageindex"
	"github.com/bborbe/vault-ui/pkg/pageindex/mocks"
)

// newStore opens a real store at a temp path and closes it when the spec ends.
func newHydrationStore(t GinkgoTInterface) pageindex.Store {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "page-index.bolt")
	warns := &warnSink{}
	store := pageindex.NewBoltStore(
		ctx,
		path,
		pageindex.CurrentWriterIdentity(ctx),
		warns.warnf,
		func(format string, args ...any) {},
	)
	DeferCleanup(func() {
		Expect(store.Close()).To(Succeed())
	})
	return store
}

// populateStore lists the folder with the production seams and writes one
// stored entry per listed file, standing in for prompt 3's write-through. Every
// listed file is stored, including the excluded ones, which are stored as
// fingerprint-only entries so an unchanged excluded file is neither re-read nor
// re-warned on the next start.
func populateStore(
	ctx context.Context,
	store pageindex.Store,
	key pageindex.Key,
	vaultDir string,
	folder string,
) {
	lister := pageindex.NewDirectoryLister()
	reader := pageindex.NewPageReader(storage.NewPageStorage(nil))
	entries, err := lister.ListFiles(ctx, vaultDir, folder)
	Expect(err).NotTo(HaveOccurred())

	puts := make([]pageindex.StoredEntry, 0, len(entries))
	for _, entry := range entries {
		page, fingerprint, readErr := reader.ReadPage(ctx, vaultDir, folder, entry.Name)
		stored := pageindex.StoredEntry{Filename: entry.Name, Fingerprint: fingerprint}
		if readErr == nil {
			stored.Page = page
		}
		puts = append(puts, stored)
	}
	Expect(store.Write(ctx, key, puts, nil)).To(Succeed())
}

// newStoreIndex builds a store-backed index with captured warnings.
func newStoreIndex(
	store pageindex.Store,
	reader pageindex.PageReader,
	lister pageindex.DirectoryLister,
	warns *warnSink,
) pageindex.PageIndex {
	return pageindex.NewPageIndexWithStoreAndWarnf(
		reader, lister, libtime.NewCurrentDateTime(), fastWaiter(), store, warns.warnf,
	)
}

// storeStart is one process start: fresh seams over a shared store.
type storeStart struct {
	reader *recordingReader
	lister *recordingLister
	index  pageindex.PageIndex
}

// newStoreStart builds a fresh start over the store with counting seams.
func newStoreStart(store pageindex.Store) *storeStart {
	reader := &recordingReader{inner: pageindex.NewPageReader(storage.NewPageStorage(nil))}
	lister := &recordingLister{inner: pageindex.NewDirectoryLister()}
	return &storeStart{
		reader: reader,
		lister: lister,
		index:  newStoreIndex(store, reader, lister, &warnSink{}),
	}
}

// newStoreVault builds a real vault folder holding n parseable pages named
// Page00.md upward, so their names sort in listing order.
func newStoreVault(n int) string {
	vaultDir := GinkgoT().TempDir()
	Expect(os.MkdirAll(
		filepath.Join(vaultDir, equivalenceFolder), 0750,
	)).To(Succeed())
	for i := 0; i < n; i++ {
		writeFixtureFile(vaultDir, fmt.Sprintf("Page%02d.md", i), fmt.Sprintf(
			"---\ntitle: Page%02d\n---\n# Page%02d\n", i, i,
		))
	}
	return vaultDir
}

// blockingLister blocks every listing until the test closes release.
type blockingLister struct {
	inner   pageindex.DirectoryLister
	release chan struct{}
}

func (l *blockingLister) ListFiles(
	ctx context.Context,
	vaultPath string,
	pagesDir string,
) ([]pageindex.FileEntry, error) {
	<-l.release
	return l.inner.ListFiles(ctx, vaultPath, pagesDir)
}

// togglableLister fails every listing while err is set.
type togglableLister struct {
	inner pageindex.DirectoryLister

	mu  sync.Mutex
	err error
}

func (l *togglableLister) ListFiles(
	ctx context.Context,
	vaultPath string,
	pagesDir string,
) ([]pageindex.FileEntry, error) {
	l.mu.Lock()
	err := l.err
	l.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return l.inner.ListFiles(ctx, vaultPath, pagesDir)
}

func (l *togglableLister) setErr(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.err = err
}

// listResult carries one ListPages outcome out of a goroutine.
type listResult struct {
	pages []*domain.Page
	err   error
}

var _ = Describe("Store hydration", func() {
	It("AC1: the store round-trip is lossless", func() {
		ctx := context.Background()
		vaultDir := buildEquivalenceVault()
		key := pageindex.NewKey(vaultDir, equivalenceFolder)

		store := newHydrationStore(GinkgoT())
		populateStore(ctx, store, key, vaultDir, equivalenceFolder)

		second := newStoreStart(store)
		got, err := second.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())

		want, err := storage.NewPageStorage(nil).ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		// Positive control: a fixture that silently excluded everything could
		// not pass this.
		Expect(indexedNames(want)).To(Equal([]string{"Inside", "Plain", "Wikilink", "Ünïcode"}))
		Expect(reflect.DeepEqual(got, want)).To(BeTrue())
		Expect(second.reader.Count()).To(Equal(0))
		Expect(second.lister.Count()).To(Equal(1))

		// Change a file that is not the fixture's in-vault symlink target:
		// statFingerprint follows symlinks, so rewriting Plain.md would change
		// Inside.md's fingerprint too and the third start would read 2 files.
		settle()
		writeFixtureFile(
			vaultDir,
			"Wikilink.md",
			"---\ntitle: Wikilink\nrelated: [[Other]]\n---\n# Wikilink v2\n",
		)

		third := newStoreStart(store)
		got, err = third.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(third.reader.Names()).To(Equal([]string{"Wikilink.md"}))

		want, err = storage.NewPageStorage(nil).ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(reflect.DeepEqual(got, want)).To(BeTrue())
	})

	It("AC2: a stored snapshot is never served before its stat-diff", func() {
		ctx := context.Background()
		vaultDir := buildEquivalenceVault()
		key := pageindex.NewKey(vaultDir, equivalenceFolder)

		store := newHydrationStore(GinkgoT())
		populateStore(ctx, store, key, vaultDir, equivalenceFolder)

		reader := &recordingReader{inner: pageindex.NewPageReader(storage.NewPageStorage(nil))}
		lister := &blockingLister{
			inner:   pageindex.NewDirectoryLister(),
			release: make(chan struct{}),
		}
		index := newStoreIndex(store, reader, lister, &warnSink{})

		done := make(chan listResult, 1)
		go func() {
			defer GinkgoRecover()
			pages, err := index.ListPages(ctx, vaultDir, equivalenceFolder)
			done <- listResult{pages: pages, err: err}
		}()

		// The stored snapshot exists from the store read, but the read must not
		// return while the stat-diff's listing is blocked.
		Consistently(func() int { return len(done) }, "150ms").Should(Equal(0))

		close(lister.release)
		var served listResult
		Eventually(done, "2s").Should(Receive(&served))
		Expect(served.err).NotTo(HaveOccurred())
		Expect(served.pages).To(HaveLen(4))
		Expect(reader.Count()).To(Equal(0))
	})

	It("AC2 control: a key with no stored entries blocks on the full parse", func() {
		ctx := context.Background()
		store := newHydrationStore(GinkgoT())
		// A different vault dir has no stored entries for its key.
		controlDir := newStoreVault(3)

		reader := &recordingReader{inner: pageindex.NewPageReader(storage.NewPageStorage(nil))}
		lister := &blockingLister{
			inner:   pageindex.NewDirectoryLister(),
			release: make(chan struct{}),
		}
		index := newStoreIndex(store, reader, lister, &warnSink{})

		done := make(chan listResult, 1)
		go func() {
			defer GinkgoRecover()
			pages, err := index.ListPages(ctx, controlDir, equivalenceFolder)
			done <- listResult{pages: pages, err: err}
		}()

		Consistently(func() int { return len(done) }, "150ms").Should(Equal(0))

		close(lister.release)
		var served listResult
		Eventually(done, "2s").Should(Receive(&served))
		Expect(served.err).NotTo(HaveOccurred())

		want, err := storage.NewPageStorage(nil).ListPages(ctx, controlDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(reflect.DeepEqual(served.pages, want)).To(BeTrue())
		Expect(reader.Count()).To(Equal(3))
	})

	It("AC3(a): an unchanged folder lists once and reads nothing", func() {
		ctx := context.Background()
		vaultDir := newStoreVault(6)
		key := pageindex.NewKey(vaultDir, equivalenceFolder)
		store := newHydrationStore(GinkgoT())
		populateStore(ctx, store, key, vaultDir, equivalenceFolder)

		start := newStoreStart(store)
		before := pageindex.FilesReadTotal("build")
		pages, err := start.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(indexedNames(pages)).To(HaveLen(6))
		Expect(start.lister.Count()).To(Equal(1))
		Expect(start.reader.Count()).To(Equal(0))
		Expect(pageindex.FilesReadTotal("build") - before).To(Equal(float64(0)))
	})

	It("AC3(b): re-reads exactly the K changed files", func() {
		ctx := context.Background()
		vaultDir := newStoreVault(6)
		key := pageindex.NewKey(vaultDir, equivalenceFolder)
		store := newHydrationStore(GinkgoT())
		populateStore(ctx, store, key, vaultDir, equivalenceFolder)

		changed := []string{"Page01.md", "Page03.md", "Page05.md"}
		settle()
		for i, name := range changed {
			writeFixtureFile(vaultDir, name, fmt.Sprintf(
				"---\ntitle: Changed%d\n---\n# Changed%d\n", i, i,
			))
		}

		start := newStoreStart(store)
		before := pageindex.FilesReadTotal("build")
		pages, err := start.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(pages).To(HaveLen(6))
		Expect(start.lister.Count()).To(Equal(1))
		Expect(start.reader.Names()).To(ConsistOf(changed))
		Expect(pageindex.FilesReadTotal("build") - before).To(Equal(float64(3)))
	})

	It("AC3(c): re-reads only the added file and drops the removed one", func() {
		ctx := context.Background()
		vaultDir := newStoreVault(5)
		key := pageindex.NewKey(vaultDir, equivalenceFolder)
		store := newHydrationStore(GinkgoT())
		populateStore(ctx, store, key, vaultDir, equivalenceFolder)

		settle()
		writeFixtureFile(vaultDir, "Page05.md", "---\ntitle: Page05\n---\n# Page05\n")
		Expect(os.Remove(pageFile(vaultDir, "Page04.md"))).To(Succeed())

		start := newStoreStart(store)
		before := pageindex.FilesReadTotal("build")
		pages, err := start.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(start.reader.Names()).To(Equal([]string{"Page05.md"}))
		Expect(indexedNames(pages)).To(ConsistOf(
			"Page00", "Page01", "Page02", "Page03", "Page05",
		))

		want, err := storage.NewPageStorage(nil).ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(reflect.DeepEqual(pages, want)).To(BeTrue())
		Expect(pageindex.FilesReadTotal("build") - before).To(Equal(float64(1)))
	})

	It("AC3(d): detects a same-size rewrite with a restored modification time", func() {
		ctx := context.Background()
		vaultDir := newStoreVault(5)
		key := pageindex.NewKey(vaultDir, equivalenceFolder)
		store := newHydrationStore(GinkgoT())
		populateStore(ctx, store, key, vaultDir, equivalenceFolder)

		target := pageFile(vaultDir, "Page02.md")
		info, err := os.Stat(target)
		Expect(err).NotTo(HaveOccurred())

		settle()
		original := "---\ntitle: Page02\n---\n# Page02\n"
		replacement := "---\ntitle: Page02\n---\n# Page99\n"
		Expect(len(replacement)).To(Equal(len(original)))
		writeFixtureFile(vaultDir, "Page02.md", replacement)
		Expect(os.Chtimes(target, info.ModTime(), info.ModTime())).To(Succeed())

		start := newStoreStart(store)
		before := pageindex.FilesReadTotal("build")
		pages, err := start.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(start.reader.Names()).To(Equal([]string{"Page02.md"}))
		Expect(pageindex.FilesReadTotal("build") - before).To(Equal(float64(1)))
		Expect(string(pages[2].Content)).To(ContainSubstring("# Page99"))
	})

	It("AC4: falls back to a full parse when the store supplies nothing", func() {
		ctx := context.Background()
		vaultDir := newStoreVault(3)

		fake := &mocks.Store{}
		fake.LoadReturns(nil, false, nil)
		reader := &recordingReader{inner: pageindex.NewPageReader(storage.NewPageStorage(nil))}
		lister := &recordingLister{inner: pageindex.NewDirectoryLister()}
		warns := &warnSink{}
		index := newStoreIndex(fake, reader, lister, warns)

		got, err := index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		want, err := storage.NewPageStorage(nil).ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(reflect.DeepEqual(got, want)).To(BeTrue())
		Expect(reader.Count()).To(Equal(3))
		Expect(warns.naming("")).To(BeEmpty())
	})

	It("AC4: serves correctly when the store read fails", func() {
		ctx := context.Background()
		vaultDir := newStoreVault(3)

		fake := &mocks.Store{}
		fake.LoadReturns(nil, false, errors.New("boom"))
		reader := &recordingReader{inner: pageindex.NewPageReader(storage.NewPageStorage(nil))}
		lister := &recordingLister{inner: pageindex.NewDirectoryLister()}
		warns := &warnSink{}
		index := newStoreIndex(fake, reader, lister, warns)

		got, err := index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		want, err := storage.NewPageStorage(nil).ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(reflect.DeepEqual(got, want)).To(BeTrue())
		Expect(reader.Count()).To(Equal(3))
		Expect(warns.naming("")).To(BeEmpty())
	})

	It("AC2/failure: a failed stat-diff of a store-loaded key publishes nothing", func() {
		ctx := context.Background()
		vaultDir := newStoreVault(4)
		key := pageindex.NewKey(vaultDir, equivalenceFolder)
		store := newHydrationStore(GinkgoT())
		populateStore(ctx, store, key, vaultDir, equivalenceFolder)

		reader := &recordingReader{inner: pageindex.NewPageReader(storage.NewPageStorage(nil))}
		lister := &togglableLister{inner: pageindex.NewDirectoryLister()}
		index := newStoreIndex(store, reader, lister, &warnSink{})

		lister.setErr(errors.New("listing boom"))
		_, err := index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(HaveOccurred())

		// The next read takes the cold path, since the store was already
		// consulted and the seeded baseline was discarded.
		lister.setErr(nil)
		pages, err := index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(indexedNames(pages)).To(HaveLen(4))

		want, err := storage.NewPageStorage(nil).ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(reflect.DeepEqual(pages, want)).To(BeTrue())
	})
})
