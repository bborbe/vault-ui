// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/pageindex"
)

// recordingReader counts and names the single-file reads the index makes.
type recordingReader struct {
	inner pageindex.PageReader

	mu    sync.Mutex
	count int
	names []string
}

func (r *recordingReader) ReadPage(
	ctx context.Context,
	vaultPath string,
	pagesDir string,
	filename string,
) (*domain.Page, pageindex.FileFingerprint, error) {
	r.mu.Lock()
	r.count++
	r.names = append(r.names, filename)
	r.mu.Unlock()
	return r.inner.ReadPage(ctx, vaultPath, pagesDir, filename)
}

func (r *recordingReader) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

func (r *recordingReader) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.names...)
}

func (r *recordingReader) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count = 0
	r.names = nil
}

// recordingLister counts the folder listings the index makes.
type recordingLister struct {
	inner pageindex.DirectoryLister

	mu    sync.Mutex
	count int
}

func (l *recordingLister) ListFiles(
	ctx context.Context,
	vaultPath string,
	pagesDir string,
) ([]pageindex.FileEntry, error) {
	l.mu.Lock()
	l.count++
	l.mu.Unlock()
	return l.inner.ListFiles(ctx, vaultPath, pagesDir)
}

func (l *recordingLister) Count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.count
}

func (l *recordingLister) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.count = 0
}

// warnSink captures the index's per-file warnings.
type warnSink struct {
	mu    sync.Mutex
	lines []string
}

func (w *warnSink) warnf(format string, args ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lines = append(w.lines, fmt.Sprintf(format, args...))
}

// naming returns the captured warning lines that mention substr.
func (w *warnSink) naming(substr string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	matched := make([]string, 0, len(w.lines))
	for _, line := range w.lines {
		if strings.Contains(line, substr) {
			matched = append(matched, line)
		}
	}
	return matched
}

// realIndex is the index under test over the production seams, with the
// seams' calls recorded and the warnings captured.
type realIndex struct {
	reader *recordingReader
	lister *recordingLister
	warns  *warnSink
	clock  libtime.CurrentDateTime
	index  pageindex.PageIndex
}

func newRealIndex() *realIndex {
	reader := &recordingReader{inner: pageindex.NewPageReader()}
	lister := &recordingLister{inner: pageindex.NewDirectoryLister()}
	warns := &warnSink{}
	clock := libtime.NewCurrentDateTime()
	return &realIndex{
		reader: reader,
		lister: lister,
		warns:  warns,
		clock:  clock,
		index: pageindex.NewPageIndexWithWarnf(
			reader, lister, clock, fastWaiter(), warns.warnf,
		),
	}
}

// pageFile is the on-disk path of one fixture file.
func pageFile(vaultDir, name string) string {
	return filepath.Join(vaultDir, equivalenceFolder, name)
}

// settle waits long enough for a fresh write to move a file's status-change
// time past the granularity the recorded fingerprint can see.
func settle() {
	time.Sleep(20 * time.Millisecond)
}

// newStatDiffVault builds a real vault folder holding Alpha, Beta and an
// unparsable BadYaml.
func newStatDiffVault() string {
	vaultDir := GinkgoT().TempDir()
	Expect(os.MkdirAll(
		filepath.Join(vaultDir, equivalenceFolder), 0750,
	)).To(Succeed())
	writeFixtureFile(vaultDir, "Alpha.md", "---\ntitle: Alpha\n---\n# Alpha A\n")
	writeFixtureFile(vaultDir, "Beta.md", "---\ntitle: Beta\n---\n# Beta\n")
	writeFixtureFile(vaultDir, "BadYaml.md", "---\ntitle: [unclosed\n---\n# Bad\n")
	return vaultDir
}

var _ = Describe("Stat-diff rescan", func() {
	var (
		ctx      context.Context
		vaultDir string
		ri       *realIndex
		key      pageindex.Key
	)

	BeforeEach(func() {
		ctx = context.Background()
		vaultDir = newStatDiffVault()
		ri = newRealIndex()
		key = pageindex.NewKey(vaultDir, equivalenceFolder)
	})

	It("AC4(a) reads nothing and republishes nothing when nothing changed", func() {
		start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		ri.clock.SetNow(libtime.DateTime(start))

		before, err := ri.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(BeNil())
		Expect(indexedNames(before)).To(Equal([]string{"Alpha", "Beta"}))
		Expect(ri.lister.Count()).To(Equal(1))
		// The unparsable file was warned about once, by the cold build.
		Expect(ri.warns.naming("BadYaml.md")).To(HaveLen(1))

		ri.reader.reset()
		ri.lister.reset()

		rescanCtx, cancel := context.WithCancel(ctx)
		rescanDone := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			rescanDone <- ri.index.Rescan(rescanCtx)
		}()
		// Let the loop record its start time before the clock moves.
		Consistently(ri.lister.Count, "100ms").Should(Equal(0))

		ri.clock.SetNow(libtime.DateTime(start.Add(time.Minute)))
		Eventually(ri.lister.Count, "2s").Should(Equal(1))
		Consistently(ri.reader.Count, "100ms").Should(Equal(0))

		after, err := ri.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(BeNil())
		Expect(len(after)).To(Equal(len(before)))
		Expect(&after[0]).To(BeIdenticalTo(&before[0]))
		Expect(ri.lister.Count()).To(Equal(1))
		Expect(ri.warns.naming("BadYaml.md")).To(HaveLen(1))

		// A second rescan of the still-unchanged folder reads and warns nothing
		// new either.
		ri.clock.SetNow(libtime.DateTime(start.Add(2 * time.Minute)))
		Eventually(ri.lister.Count, "2s").Should(Equal(2))
		Consistently(ri.reader.Count, "100ms").Should(Equal(0))
		Expect(ri.lister.Count()).To(Equal(2))
		Expect(ri.warns.naming("BadYaml.md")).To(HaveLen(1))

		cancel()
		Eventually(rescanDone, "2s").Should(Receive(BeNil()))
	})

	It("AC4(b) re-reads exactly the modified and the added file", func() {
		_, err := ri.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(BeNil())
		ri.reader.reset()
		ri.lister.reset()

		settle()
		writeFixtureFile(vaultDir, "Alpha.md", "---\ntitle: Alpha\n---\n# Alpha changed\n")
		writeFixtureFile(vaultDir, "Gamma.md", "---\ntitle: Gamma\n---\n# Gamma\n")
		Expect(os.Remove(pageFile(vaultDir, "Beta.md"))).To(Succeed())

		Expect(ri.index.Refresh(ctx, key)).To(BeNil())
		Expect(ri.lister.Count()).To(Equal(1))
		Expect(ri.reader.Names()).To(ConsistOf("Alpha.md", "Gamma.md"))

		pages, err := ri.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(BeNil())
		Expect(indexedNames(pages)).To(Equal([]string{"Alpha", "Gamma"}))
	})

	It("AC4(c) detects a same-size rewrite whose modification time was restored", func() {
		_, err := ri.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(BeNil())
		ri.reader.reset()
		ri.lister.reset()

		alpha := pageFile(vaultDir, "Alpha.md")
		info, err := os.Stat(alpha)
		Expect(err).To(BeNil())

		settle()
		// Same byte count, different content.
		writeFixtureFile(vaultDir, "Alpha.md", "---\ntitle: Alpha\n---\n# Alpha B\n")
		Expect(os.Chtimes(alpha, info.ModTime(), info.ModTime())).To(Succeed())

		Expect(ri.index.Refresh(ctx, key)).To(BeNil())
		Expect(ri.reader.Names()).To(Equal([]string{"Alpha.md"}))

		pages, err := ri.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(BeNil())
		Expect(string(pages[0].Content)).To(ContainSubstring("# Alpha B"))
	})

	It("AC5(ii)/8 lists once and re-reads only what changed after a folder mark", func() {
		_, err := ri.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(BeNil())
		ri.reader.reset()
		ri.lister.reset()

		// Nothing changed: the mark costs one listing and no read.
		ri.index.MarkDirty(key)
		pages, err := ri.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(BeNil())
		Expect(indexedNames(pages)).To(Equal([]string{"Alpha", "Beta"}))
		Expect(ri.lister.Count()).To(Equal(1))
		Expect(ri.reader.Count()).To(Equal(0))

		ri.reader.reset()
		ri.lister.reset()
		settle()
		writeFixtureFile(vaultDir, "Beta.md", "---\ntitle: Beta\n---\n# Beta changed\n")
		ri.index.MarkDirty(key)
		pages, err = ri.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(BeNil())
		Expect(indexedNames(pages)).To(Equal([]string{"Alpha", "Beta"}))
		Expect(ri.lister.Count()).To(Equal(1))
		Expect(ri.reader.Names()).To(Equal([]string{"Beta.md"}))
	})

	It("AC4/failure 9 keeps the snapshot and the mark pending when the listing fails", func() {
		fake := newStorageFake()
		fakeKey := pageindex.NewKey("/vault-a", "24 Tasks")
		fake.setPages(fakeKey, "old")
		index := newIndex(fake)
		_, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
		Expect(err).To(BeNil())

		index.MarkDirty(fakeKey)
		fake.setFailure(fmt.Errorf("listing boom"))

		Expect(index.Refresh(ctx, fakeKey)).To(BeNil())
		Expect(fake.lister.ListFilesCallCount()).To(Equal(2))

		// The mark is still pending, so the next read lists again.
		pages, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
		Expect(err).To(BeNil())
		Expect(titles(pages)).To(Equal([]string{"old"}))
		Expect(fake.lister.ListFilesCallCount()).To(Equal(3))

		fake.setFailure(nil)
		fake.setPages(fakeKey, "new")
		pages, err = index.ListPages(ctx, "/vault-a", "24 Tasks")
		Expect(err).To(BeNil())
		Expect(titles(pages)).To(Equal([]string{"new"}))
		Expect(fake.lister.ListFilesCallCount()).To(Equal(4))
	})

	It("empties and refills the snapshot when the folder is deleted and recreated", func() {
		pages, err := ri.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(BeNil())
		Expect(pages).NotTo(BeEmpty())

		Expect(os.RemoveAll(filepath.Join(vaultDir, equivalenceFolder))).To(Succeed())
		Expect(ri.index.Refresh(ctx, key)).To(BeNil())
		pages, err = ri.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(BeNil())
		Expect(pages).To(BeNil())

		Expect(os.MkdirAll(
			filepath.Join(vaultDir, equivalenceFolder), 0750,
		)).To(Succeed())
		writeFixtureFile(vaultDir, "Delta.md", "---\ntitle: Delta\n---\n# Delta\n")
		Expect(ri.index.Refresh(ctx, key)).To(BeNil())
		pages, err = ri.index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).To(BeNil())
		Expect(indexedNames(pages)).To(Equal([]string{"Delta"}))
	})
})
