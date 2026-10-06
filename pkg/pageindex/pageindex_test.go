// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex_test

import (
	"context"
	stderrors "errors"
	"strings"
	"sync"
	"time"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/pageindex"
	pageindexmocks "github.com/bborbe/vault-ui/pkg/pageindex/mocks"
)

// storageFake is a reader and lister pair whose per-key content and blocking
// behaviour the tests control. The counterfeiter fakes underneath record every
// call.
type storageFake struct {
	reader *pageindexmocks.PageReader
	lister *pageindexmocks.DirectoryLister

	mu       sync.Mutex
	pages    map[pageindex.Key][]*domain.Page
	versions map[pageindex.Key]map[string]int64
	listed   map[pageindex.Key][]fakeFile
	gate     chan struct{}
	entered  chan struct{}
	failure  error
}

// fakeFile is one page file captured by a listing.
type fakeFile struct {
	name    string
	page    *domain.Page
	version int64
}

func newStorageFake() *storageFake {
	fake := &storageFake{
		reader:   &pageindexmocks.PageReader{},
		lister:   &pageindexmocks.DirectoryLister{},
		pages:    map[pageindex.Key][]*domain.Page{},
		versions: map[pageindex.Key]map[string]int64{},
		listed:   map[pageindex.Key][]fakeFile{},
	}
	fake.reader.ReadPageStub = fake.readPage
	fake.lister.ListFilesStub = fake.listFiles
	return fake
}

// fingerprint derives a file's fingerprint from its version counter, so a
// content change always changes the fingerprint.
func fingerprint(version int64) pageindex.FileFingerprint {
	return pageindex.FileFingerprint{
		Size:    version,
		ModTime: time.Unix(version, 0).UTC(),
	}
}

func (f *storageFake) listFiles(
	_ context.Context,
	vaultPath string,
	pagesDir string,
) ([]pageindex.FileEntry, error) {
	key := pageindex.NewKey(vaultPath, pagesDir)

	f.mu.Lock()
	gate, entered := f.gate, f.entered
	live := f.pages[key]
	captured := make([]fakeFile, 0, len(live))
	for _, page := range live {
		name := page.FileMetadata.Name
		captured = append(captured, fakeFile{
			name:    name,
			page:    page,
			version: f.versions[key][name],
		})
	}
	f.listed[key] = captured
	f.mu.Unlock()

	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if gate != nil {
		<-gate
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failure != nil {
		return nil, f.failure
	}
	entries := make([]pageindex.FileEntry, 0, len(captured))
	for _, file := range captured {
		entries = append(entries, pageindex.FileEntry{
			Name:        file.name + ".md",
			Fingerprint: fingerprint(file.version),
		})
	}
	return entries, nil
}

func (f *storageFake) readPage(
	_ context.Context,
	vaultPath string,
	pagesDir string,
	filename string,
) (*domain.Page, pageindex.FileFingerprint, error) {
	key := pageindex.NewKey(vaultPath, pagesDir)
	name := strings.TrimSuffix(filename, ".md")
	f.mu.Lock()
	captured := f.listed[key]
	f.mu.Unlock()
	for _, file := range captured {
		if file.name == name {
			return file.page, fingerprint(file.version), nil
		}
	}
	return nil, pageindex.FileFingerprint{}, stderrors.New("page not listed: " + name)
}

// block makes every listing wait until the returned release func runs. The
// returned channel reports each call that entered the stub.
func (f *storageFake) block() (<-chan struct{}, func()) {
	gate := make(chan struct{})
	entered := make(chan struct{}, 64)
	f.mu.Lock()
	f.gate = gate
	f.entered = entered
	f.mu.Unlock()
	return entered, func() {
		f.mu.Lock()
		f.gate = nil
		f.entered = nil
		f.mu.Unlock()
		close(gate)
	}
}

func (f *storageFake) setPages(key pageindex.Key, titles ...string) {
	key = pageindex.NewKey(key.VaultPath, key.PagesDir)
	pages := make([]*domain.Page, 0, len(titles))
	for _, title := range titles {
		pages = append(pages, newPage(title))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	previous := map[string]*domain.Page{}
	for _, page := range f.pages[key] {
		previous[page.FileMetadata.Name] = page
	}
	versions := f.versions[key]
	if versions == nil {
		versions = map[string]int64{}
		f.versions[key] = versions
	}
	for _, page := range pages {
		name := page.FileMetadata.Name
		if old, ok := previous[name]; !ok || old.Content != page.Content {
			versions[name]++
		}
	}
	f.pages[key] = pages
}

func (f *storageFake) setFailure(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failure = err
}

func newPage(title string) *domain.Page {
	return domain.NewPage(
		map[string]any{"title": title},
		domain.FileMetadata{Name: title, FilePath: "/vault/" + title + ".md"},
		domain.Content("# "+title),
	)
}

func titles(pages []*domain.Page) []string {
	result := make([]string, 0, len(pages))
	for _, page := range pages {
		result = append(result, page.GetString("title"))
	}
	return result
}

func callCountFor(fake *storageFake, key pageindex.Key) int {
	want := pageindex.NewKey(key.VaultPath, key.PagesDir)
	count := 0
	for i := 0; i < fake.lister.ListFilesCallCount(); i++ {
		_, vaultPath, pagesDir := fake.lister.ListFilesArgsForCall(i)
		if pageindex.NewKey(vaultPath, pagesDir) == want {
			count++
		}
	}
	return count
}

func fastWaiter() libtime.WaiterDuration {
	return libtime.WaiterDurationFunc(func(ctx context.Context, duration libtime.Duration) error {
		timer := time.NewTimer(time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	})
}

func newIndex(fake *storageFake) pageindex.PageIndex {
	return pageindex.NewPageIndex(
		fake.reader, fake.lister, libtime.NewCurrentDateTime(), fastWaiter(),
	)
}

var _ = Describe("PageIndex", func() {
	var (
		ctx  context.Context
		fake *storageFake
	)

	BeforeEach(func() {
		ctx = context.Background()
		fake = newStorageFake()
	})

	Describe("ListPages", func() {
		It("serves repeat reads of a clean key without touching storage", func() {
			fake.setPages(pageindex.NewKey("/vault-a", "24 Tasks"), "one", "two")
			index := newIndex(fake)

			first, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(BeNil())
			Expect(titles(first)).To(Equal([]string{"one", "two"}))
			Expect(fake.lister.ListFilesCallCount()).To(Equal(1))

			for i := 0; i < 5; i++ {
				again, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
				Expect(err).To(BeNil())
				Expect(again[0]).To(BeIdenticalTo(first[0]))
			}
			Expect(fake.lister.ListFilesCallCount()).To(Equal(1))
		})

		It("shares a single build across concurrent cold reads", func() {
			key := pageindex.NewKey("/vault-a", "24 Tasks")
			fake.setPages(key, "one")
			index := newIndex(fake)
			entered, release := fake.block()

			results := make([][]*domain.Page, 8)
			errs := make([]error, 8)
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					defer GinkgoRecover()
					results[i], errs[i] = index.ListPages(ctx, "/vault-a", "24 Tasks")
				}(i)
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				Expect(index.Build(ctx, []pageindex.Key{key})).To(BeNil())
			}()

			Eventually(entered).Should(Receive())
			Consistently(fake.lister.ListFilesCallCount, "200ms").Should(Equal(1))

			release()
			wg.Wait()

			Expect(fake.lister.ListFilesCallCount()).To(Equal(1))
			for i := range results {
				Expect(errs[i]).To(BeNil())
				Expect(titles(results[i])).To(Equal([]string{"one"}))
			}
		})
	})

	Describe("Refresh", func() {
		It("rebuilds only the requested key", func() {
			aTasks := pageindex.NewKey("/vault-a", "24 Tasks")
			aGoals := pageindex.NewKey("/vault-a", "25 Goals")
			bTasks := pageindex.NewKey("/vault-b", "24 Tasks")
			bGoals := pageindex.NewKey("/vault-b", "25 Goals")
			all := []pageindex.Key{aTasks, aGoals, bTasks, bGoals}
			for _, key := range all {
				fake.setPages(key, "old")
			}
			index := newIndex(fake)
			Expect(index.Build(ctx, all)).To(BeNil())
			Expect(fake.lister.ListFilesCallCount()).To(Equal(4))

			fake.setPages(aTasks, "new")
			Expect(index.Refresh(ctx, aTasks)).To(BeNil())

			Expect(callCountFor(fake, aTasks)).To(Equal(2))
			for _, key := range []pageindex.Key{aGoals, bTasks, bGoals} {
				Expect(callCountFor(fake, key)).To(Equal(1))
			}
			pages, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(BeNil())
			Expect(titles(pages)).To(Equal([]string{"new"}))
		})

		It("keeps serving the previous snapshot while a rebuild is blocked", func() {
			key := pageindex.NewKey("/vault-a", "24 Tasks")
			fake.setPages(key, "old")
			index := newIndex(fake)
			_, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(BeNil())

			entered, release := fake.block()
			fake.setPages(key, "new")
			refreshDone := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				refreshDone <- index.Refresh(ctx, key)
			}()
			Eventually(entered).Should(Receive())

			start := time.Now()
			pages, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(BeNil())
			Expect(titles(pages)).To(Equal([]string{"old"}))
			Expect(time.Since(start)).To(BeNumerically("<", 100*time.Millisecond))

			release()
			Expect(<-refreshDone).To(BeNil())
			pages, err = index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(BeNil())
			Expect(titles(pages)).To(Equal([]string{"new"}))
		})

		It("coalesces a burst of refreshes into one follow-up", func() {
			key := pageindex.NewKey("/vault-a", "24 Tasks")
			fake.setPages(key, "old")
			index := newIndex(fake)
			entered, release := fake.block()

			first := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				first <- index.Refresh(ctx, key)
			}()
			Eventually(entered).Should(Receive())

			rest := make([]chan error, 3)
			for i := range rest {
				rest[i] = make(chan error, 1)
				done := rest[i]
				go func() {
					defer GinkgoRecover()
					done <- index.Refresh(ctx, key)
				}()
			}
			Consistently(fake.lister.ListFilesCallCount, "200ms").Should(Equal(1))

			fake.setPages(key, "new")
			release()

			Expect(<-first).To(BeNil())
			for _, done := range rest {
				Expect(<-done).To(BeNil())
				pages, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
				Expect(err).To(BeNil())
				Expect(titles(pages)).To(Equal([]string{"new"}))
			}
			Expect(fake.lister.ListFilesCallCount()).To(Equal(2))
		})
	})

	Describe("dirty marks", func() {
		It("rebuilds once on the next read and serves the new content", func() {
			key := pageindex.NewKey("/vault-a", "24 Tasks")
			fake.setPages(key, "old")
			index := newIndex(fake)
			_, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(BeNil())

			fake.setPages(key, "new")
			index.MarkDirty(key)
			Expect(fake.lister.ListFilesCallCount()).To(Equal(1))

			pages, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(BeNil())
			Expect(titles(pages)).To(Equal([]string{"new"}))
			Expect(fake.lister.ListFilesCallCount()).To(Equal(2))
		})

		It("shares one rebuild between two concurrent reads after one mark", func() {
			key := pageindex.NewKey("/vault-a", "24 Tasks")
			fake.setPages(key, "old")
			index := newIndex(fake)
			_, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(BeNil())

			fake.setPages(key, "new")
			index.MarkDirty(key)
			entered, release := fake.block()

			var wg sync.WaitGroup
			results := make([][]*domain.Page, 2)
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					defer GinkgoRecover()
					results[i], _ = index.ListPages(ctx, "/vault-a", "24 Tasks")
				}(i)
			}
			Eventually(entered).Should(Receive())
			release()
			wg.Wait()

			Expect(fake.lister.ListFilesCallCount()).To(Equal(2))
			for i := range results {
				Expect(titles(results[i])).To(Equal([]string{"new"}))
			}
		})

		It("forces a reader onto a rebuild that started after the mark", func() {
			key := pageindex.NewKey("/vault-a", "24 Tasks")
			fake.setPages(key, "old")
			index := newIndex(fake)
			_, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(BeNil())

			entered, release := fake.block()
			refreshDone := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				refreshDone <- index.Refresh(ctx, key)
			}()
			Eventually(entered).Should(Receive())

			index.MarkDirty(key)
			fake.setPages(key, "new")

			readDone := make(chan []*domain.Page, 1)
			readErr := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				pages, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
				readDone <- pages
				readErr <- err
			}()
			Consistently(fake.lister.ListFilesCallCount, "150ms").Should(Equal(2))

			release()
			Expect(<-refreshDone).To(BeNil())
			Expect(<-readErr).To(BeNil())
			Expect(titles(<-readDone)).To(Equal([]string{"new"}))
			Expect(fake.lister.ListFilesCallCount()).To(Equal(3))
		})

		It("dirties every known key on MarkAllDirty", func() {
			aTasks := pageindex.NewKey("/vault-a", "24 Tasks")
			bTasks := pageindex.NewKey("/vault-b", "24 Tasks")
			fake.setPages(aTasks, "a")
			fake.setPages(bTasks, "b")
			index := newIndex(fake)
			Expect(index.Build(ctx, []pageindex.Key{aTasks, bTasks})).To(BeNil())

			index.MarkAllDirty()
			Expect(fake.lister.ListFilesCallCount()).To(Equal(2))

			_, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(BeNil())
			_, err = index.ListPages(ctx, "/vault-b", "24 Tasks")
			Expect(err).To(BeNil())
			Expect(fake.lister.ListFilesCallCount()).To(Equal(4))
		})

		It("ignores a dirty mark for an unknown key", func() {
			index := newIndex(fake)
			index.MarkDirty(pageindex.NewKey("/unknown", "24 Tasks"))
			Expect(fake.lister.ListFilesCallCount()).To(Equal(0))
		})
	})

	Describe("failures", func() {
		It("keeps the previous snapshot and retries while the key stays dirty", func() {
			key := pageindex.NewKey("/vault-a", "24 Tasks")
			fake.setPages(key, "old")
			index := newIndex(fake)
			_, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(BeNil())

			index.MarkDirty(key)
			fake.setFailure(stderrors.New("boom"))

			pages, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(BeNil())
			Expect(titles(pages)).To(Equal([]string{"old"}))
			Expect(fake.lister.ListFilesCallCount()).To(Equal(2))

			Expect(index.Refresh(ctx, key)).To(BeNil())
			Expect(fake.lister.ListFilesCallCount()).To(Equal(3))

			fake.setFailure(nil)
			fake.setPages(key, "new")
			pages, err = index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(BeNil())
			Expect(titles(pages)).To(Equal([]string{"new"}))
			Expect(fake.lister.ListFilesCallCount()).To(Equal(4))
		})

		It("returns an error when the very first build fails", func() {
			index := newIndex(fake)
			fake.setFailure(stderrors.New("boom"))
			_, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Rescan", func() {
		It("refreshes every known key once the interval has passed", func() {
			clock := libtime.NewCurrentDateTime()
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			clock.SetNow(libtime.DateTime(start))

			aTasks := pageindex.NewKey("/vault-a", "24 Tasks")
			bTasks := pageindex.NewKey("/vault-b", "24 Tasks")
			fake.setPages(aTasks, "a")
			fake.setPages(bTasks, "b")
			index := pageindex.NewPageIndex(fake.reader, fake.lister, clock, fastWaiter())
			Expect(index.Build(ctx, []pageindex.Key{aTasks, bTasks})).To(BeNil())
			Expect(fake.lister.ListFilesCallCount()).To(Equal(2))

			rescanCtx, cancel := context.WithCancel(ctx)
			rescanDone := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				rescanDone <- index.Rescan(rescanCtx)
			}()

			Consistently(fake.lister.ListFilesCallCount, "100ms").Should(Equal(2))

			fake.setPages(aTasks, "a2")
			fake.setPages(bTasks, "b2")
			clock.SetNow(libtime.DateTime(start.Add(time.Minute)))

			Eventually(fake.lister.ListFilesCallCount, "2s").Should(Equal(4))
			Eventually(func() []string {
				pages, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
				Expect(err).To(BeNil())
				return titles(pages)
			}, "2s").Should(Equal([]string{"a2"}))
			Consistently(fake.lister.ListFilesCallCount, "100ms").Should(Equal(4))

			cancel()
			Eventually(rescanDone, "2s").Should(Receive(BeNil()))
		})

		It("keeps the rescan interval inside the 60s ceiling", func() {
			Expect(pageindex.RescanInterval).To(BeNumerically("<=", 60*time.Second))
		})
	})

	Describe("context cancellation", func() {
		It("fails a waiting reader without aborting the build it waits on", func() {
			key := pageindex.NewKey("/vault-a", "24 Tasks")
			fake.setPages(key, "old")
			index := newIndex(fake)
			_, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(BeNil())

			index.MarkDirty(key)
			fake.setPages(key, "new")
			entered, release := fake.block()

			builderDone := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				_, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
				builderDone <- err
			}()
			Eventually(entered).Should(Receive())

			readCtx, cancelRead := context.WithCancel(ctx)
			readDone := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				_, err := index.ListPages(readCtx, "/vault-a", "24 Tasks")
				readDone <- err
			}()
			Consistently(fake.lister.ListFilesCallCount, "100ms").Should(Equal(2))
			cancelRead()
			Eventually(readDone).Should(Receive(HaveOccurred()))

			release()
			Expect(<-builderDone).To(BeNil())
			pages, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(BeNil())
			Expect(titles(pages)).To(Equal([]string{"new"}))
		})

		It("runs an owned follow-up even when the caller's ctx is cancelled", func() {
			key := pageindex.NewKey("/vault-a", "24 Tasks")
			fake.setPages(key, "old")
			index := newIndex(fake)
			_, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(BeNil())

			entered, release := fake.block()
			fake.setPages(key, "new")
			refreshDone := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				refreshDone <- index.Refresh(ctx, key)
			}()
			Eventually(entered).Should(Receive())

			index.MarkDirty(key)

			readCtx, cancelRead := context.WithCancel(ctx)
			readDone := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				_, err := index.ListPages(readCtx, "/vault-a", "24 Tasks")
				readDone <- err
			}()
			Consistently(fake.lister.ListFilesCallCount, "150ms").Should(Equal(2))

			cancelRead()
			release()
			Expect(<-refreshDone).To(BeNil())
			Eventually(readDone, "2s").Should(Receive(HaveOccurred()))

			Expect(fake.lister.ListFilesCallCount()).To(Equal(3))
			pages, err := index.ListPages(ctx, "/vault-a", "24 Tasks")
			Expect(err).To(BeNil())
			Expect(titles(pages)).To(Equal([]string{"new"}))
			Expect(fake.lister.ListFilesCallCount()).To(Equal(3))
		})

		It("returns an error from Build when ctx is already cancelled", func() {
			index := newIndex(fake)
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			err := index.Build(cancelled, []pageindex.Key{pageindex.NewKey("/vault-a", "24 Tasks")})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("NewKey", func() {
		It("cleans both parts so equivalent paths share a key", func() {
			Expect(pageindex.NewKey("/v/", "24 Tasks/")).To(Equal(pageindex.NewKey("/v", "24 Tasks")))
		})
	})
})
