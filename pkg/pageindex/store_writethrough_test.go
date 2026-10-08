// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/pageindex"
)

// storeWrite is one recorded Store.Write call: the entries it put and the names
// it deleted, exactly as the caller passed them.
type storeWrite struct {
	puts    []pageindex.StoredEntry
	deletes []string
}

// recordingStore is a Store seam that records every transaction and keeps an
// in-memory per-key map so Load reflects what was applied. It can be configured
// to fail or to block a write, so the write-through path is observable.
type recordingStore struct {
	mu      sync.Mutex
	entries map[pageindex.Key]map[string]pageindex.StoredEntry
	writes  []storeWrite

	fail         error
	blockWrites  bool
	writeEntered chan struct{}
	writeRelease chan struct{}
}

func (s *recordingStore) Load(
	ctx context.Context,
	key pageindex.Key,
) ([]pageindex.StoredEntry, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	key = pageindex.NewKey(key.VaultPath, key.PagesDir)
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := s.entries[key]
	if len(stored) == 0 {
		return nil, false, nil
	}
	names := make([]string, 0, len(stored))
	for name := range stored {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]pageindex.StoredEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, stored[name])
	}
	return entries, true, nil
}

func (s *recordingStore) Write(
	ctx context.Context,
	key pageindex.Key,
	puts []pageindex.StoredEntry,
	deletes []string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key = pageindex.NewKey(key.VaultPath, key.PagesDir)

	s.mu.Lock()
	s.writes = append(s.writes, storeWrite{
		puts:    append([]pageindex.StoredEntry(nil), puts...),
		deletes: append([]string(nil), deletes...),
	})
	block := s.blockWrites
	entered := s.writeEntered
	release := s.writeRelease
	fail := s.fail
	s.mu.Unlock()

	if block {
		if entered != nil {
			entered <- struct{}{}
		}
		<-release
	}
	if fail != nil {
		return fail
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = map[pageindex.Key]map[string]pageindex.StoredEntry{}
	}
	if s.entries[key] == nil {
		s.entries[key] = map[string]pageindex.StoredEntry{}
	}
	for _, put := range puts {
		s.entries[key][put.Filename] = put
	}
	for _, name := range deletes {
		delete(s.entries[key], name)
	}
	return nil
}

func (s *recordingStore) Path() string { return "recording-store" }

func (s *recordingStore) Close() error { return nil }

// recorded returns every Write call made so far, in order.
func (s *recordingStore) recorded() []storeWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storeWrite(nil), s.writes...)
}

// stored returns a copy of the in-memory map for one key.
func (s *recordingStore) stored(key pageindex.Key) map[string]pageindex.StoredEntry {
	key = pageindex.NewKey(key.VaultPath, key.PagesDir)
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := map[string]pageindex.StoredEntry{}
	for name, entry := range s.entries[key] {
		stored[name] = entry
	}
	return stored
}

// reset forgets the recorded transactions but keeps the applied content.
func (s *recordingStore) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = nil
}

// setFail makes every subsequent Write fail with err.
func (s *recordingStore) setFail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = err
}

// setBlocking makes every subsequent Write signal entered and wait until
// release is closed.
func (s *recordingStore) setBlocking(entered, release chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blockWrites = true
	s.writeEntered = entered
	s.writeRelease = release
}

// putNames returns the filenames of one write's puts.
func putNames(writes []storeWrite, i int) []string {
	names := make([]string, 0, len(writes[i].puts))
	for _, put := range writes[i].puts {
		names = append(names, put.Filename)
	}
	return names
}

// newWriteThroughIndex builds a recording-store index with counting seams.
func newWriteThroughIndex(
	store pageindex.Store,
) (*recordingReader, *recordingLister, pageindex.PageIndex) {
	reader := &recordingReader{inner: pageindex.NewPageReader(storage.NewPageStorage(nil))}
	lister := &recordingLister{inner: pageindex.NewDirectoryLister()}
	return reader, lister, newStoreIndex(store, reader, lister, &warnSink{})
}

var _ = Describe("Store write-through", func() {
	var (
		ctx   context.Context
		store *recordingStore
	)

	BeforeEach(func() {
		ctx = context.Background()
		store = &recordingStore{}
	})

	It("AC5: a cold build writes N entries in one transaction", func() {
		vaultDir := newStoreVault(5)
		key := pageindex.NewKey(vaultDir, equivalenceFolder)
		reader, _, index := newWriteThroughIndex(store)

		pages, err := index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(indexedNames(pages)).To(HaveLen(5))
		Expect(reader.Count()).To(Equal(5))

		writes := store.recorded()
		Expect(writes).To(HaveLen(1))
		Expect(putNames(writes, 0)).To(ConsistOf(
			"Page00.md", "Page01.md", "Page02.md", "Page03.md", "Page04.md",
		))
		Expect(writes[0].deletes).To(BeEmpty())
		Expect(store.stored(key)).To(HaveLen(5))
	})

	It("AC5: a per-file update writes exactly one put", func() {
		vaultDir := newStoreVault(5)
		key := pageindex.NewKey(vaultDir, equivalenceFolder)
		_, _, index := newWriteThroughIndex(store)

		_, err := index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		before := store.stored(key)
		store.reset()

		settle()
		writeFixtureFile(vaultDir, "Page02.md", "---\ntitle: Page02\n---\n# Page02 v2\n")
		Expect(index.RefreshFile(ctx, key, "Page02.md")).To(Succeed())

		writes := store.recorded()
		Expect(writes).To(HaveLen(1))
		Expect(putNames(writes, 0)).To(Equal([]string{"Page02.md"}))
		Expect(writes[0].deletes).To(BeEmpty())

		after := store.stored(key)
		Expect(after).To(HaveLen(5))
		Expect(string(after["Page02.md"].Page.Content)).To(ContainSubstring("# Page02 v2"))
		for name, entry := range before {
			if name == "Page02.md" {
				continue
			}
			Expect(after[name].Fingerprint).To(Equal(entry.Fingerprint))
			Expect(after[name].Page).To(BeIdenticalTo(entry.Page))
		}
	})

	It("AC5: a deletion writes exactly one delete", func() {
		vaultDir := newStoreVault(5)
		key := pageindex.NewKey(vaultDir, equivalenceFolder)
		_, _, index := newWriteThroughIndex(store)

		_, err := index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		store.reset()

		Expect(os.Remove(pageFile(vaultDir, "Page02.md"))).To(Succeed())
		Expect(index.RefreshFile(ctx, key, "Page02.md")).To(Succeed())

		writes := store.recorded()
		Expect(writes).To(HaveLen(1))
		Expect(writes[0].puts).To(BeEmpty())
		Expect(writes[0].deletes).To(Equal([]string{"Page02.md"}))
		Expect(store.stored(key)).NotTo(HaveKey("Page02.md"))
	})

	It("AC5: a stat-diff that drops a removed file writes exactly one delete", func() {
		vaultDir := newStoreVault(5)
		key := pageindex.NewKey(vaultDir, equivalenceFolder)
		_, _, index := newWriteThroughIndex(store)

		_, err := index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		store.reset()

		// The listing path, never RefreshFile: the removed name is dropped by the
		// merge, so the delete must be derived before the in-place fingerprint
		// update erases the name it would have reported.
		Expect(os.Remove(pageFile(vaultDir, "Page02.md"))).To(Succeed())
		Expect(index.Refresh(ctx, key)).To(Succeed())

		writes := store.recorded()
		Expect(writes).To(HaveLen(1))
		Expect(writes[0].puts).To(BeEmpty())
		Expect(writes[0].deletes).To(Equal([]string{"Page02.md"}))
		Expect(store.stored(key)).NotTo(HaveKey("Page02.md"))
	})

	It("AC5: a failed write leaves serving and the store intact", func() {
		vaultDir := newStoreVault(5)
		key := pageindex.NewKey(vaultDir, equivalenceFolder)
		reader := &recordingReader{inner: pageindex.NewPageReader(storage.NewPageStorage(nil))}
		lister := &recordingLister{inner: pageindex.NewDirectoryLister()}
		warns := &warnSink{}
		index := newStoreIndex(store, reader, lister, warns)

		_, err := index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		before := store.stored(key)
		store.reset()

		settle()
		writeFixtureFile(vaultDir, "Page02.md", "---\ntitle: Page02\n---\n# Page02 v2\n")
		store.setFail(errors.New("disk full"))

		Expect(index.RefreshFile(ctx, key, "Page02.md")).To(Succeed())

		// The publication completed and the next read serves the new content.
		pages, err := index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(indexedNames(pages)).To(HaveLen(5))
		Expect(string(pages[2].Content)).To(ContainSubstring("# Page02 v2"))

		// The previous store content is untouched and exactly one warning names
		// the failure.
		Expect(store.stored(key)).To(Equal(before))
		Expect(warns.naming("store write failed")).To(HaveLen(1))
	})

	It("AC5: a blocking write does not block a reader", func() {
		vaultDir := newStoreVault(5)
		key := pageindex.NewKey(vaultDir, equivalenceFolder)
		_, _, index := newWriteThroughIndex(store)

		_, err := index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		store.reset()

		entered := make(chan struct{}, 1)
		release := make(chan struct{})
		store.setBlocking(entered, release)

		settle()
		writeFixtureFile(vaultDir, "Page02.md", "---\ntitle: Page02\n---\n# Page02 v2\n")
		refreshDone := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			refreshDone <- index.RefreshFile(ctx, key, "Page02.md")
		}()
		Eventually(entered, "2s").Should(Receive())

		start := time.Now()
		pages, err := index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(time.Since(start)).To(BeNumerically("<", 100*time.Millisecond))
		Expect(string(pages[2].Content)).To(ContainSubstring("# Page02 v2"))

		close(release)
		Eventually(refreshDone, "2s").Should(Receive(BeNil()))
	})

	It("AC5: an unchanged rescan writes nothing", func() {
		clock := libtime.NewCurrentDateTime()
		start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		clock.SetNow(libtime.DateTime(start))

		vaultDir := newStoreVault(4)
		reader := &recordingReader{inner: pageindex.NewPageReader(storage.NewPageStorage(nil))}
		lister := &recordingLister{inner: pageindex.NewDirectoryLister()}
		index := pageindex.NewPageIndexWithStoreAndWarnf(
			reader, lister, clock, fastWaiter(), store, (&warnSink{}).warnf,
		)

		_, err := index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		store.reset()
		lister.reset()

		rescanCtx, cancel := context.WithCancel(ctx)
		rescanDone := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			rescanDone <- index.Rescan(rescanCtx)
		}()

		// Let the loop record its start time before the clock moves.
		Consistently(func() int { return len(store.recorded()) }, "100ms").Should(Equal(0))

		clock.SetNow(libtime.DateTime(start.Add(time.Minute)))
		Eventually(lister.Count, "2s").Should(Equal(1))
		Consistently(func() int { return len(store.recorded()) }, "150ms").Should(Equal(0))
		Expect(pageindex.RescanInterval).To(Equal(50 * time.Second))

		cancel()
		Eventually(rescanDone, "2s").Should(Receive(BeNil()))
	})

	It("AC5: a hydrate's stat-diff writes only what it re-read", func() {
		vaultDir := newStoreVault(6)
		key := pageindex.NewKey(vaultDir, equivalenceFolder)
		populateStore(ctx, store, key, vaultDir, equivalenceFolder)
		store.reset()

		changed := []string{"Page01.md", "Page03.md", "Page05.md"}
		settle()
		for i, name := range changed {
			writeFixtureFile(vaultDir, name, fmt.Sprintf(
				"---\ntitle: Changed%d\n---\n# Changed%d\n", i, i,
			))
		}

		reader := &recordingReader{inner: pageindex.NewPageReader(storage.NewPageStorage(nil))}
		lister := &recordingLister{inner: pageindex.NewDirectoryLister()}
		index := newStoreIndex(store, reader, lister, &warnSink{})

		pages, err := index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(pages).To(HaveLen(6))
		Expect(reader.Names()).To(ConsistOf(changed))

		writes := store.recorded()
		Expect(writes).To(HaveLen(1))
		Expect(putNames(writes, 0)).To(ConsistOf(changed))
		Expect(writes[0].deletes).To(BeEmpty())
	})
})
