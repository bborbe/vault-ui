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

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	bolt "go.etcd.io/bbolt"

	"github.com/bborbe/vault-ui/pkg/pageindex"
)

// bodySentinel sits in a page body outside frontmatter and outside any Open
// Questions item, so its presence anywhere in memory snapshots or on disk means
// a body was retained.
const bodySentinel = "BODY-SENTINEL-9c41"

// bodyFixturePage is a task page with frontmatter, a body and an Open Questions
// section.
const bodyFixturePage = "---\nstatus: in_progress\n---\n# Task\n\n" + bodySentinel + "\n\n" +
	"## Open Questions\n\n- Which vault?\n- Answered already → **personal**\n"

// buildBodyVault writes two task pages with bodies and returns the vault dir.
func buildBodyVault() string {
	vaultDir := GinkgoT().TempDir()
	Expect(os.MkdirAll(filepath.Join(vaultDir, equivalenceFolder), 0750)).To(Succeed())
	writeFixtureFile(vaultDir, "WithQuestions.md", bodyFixturePage)
	writeFixtureFile(vaultDir, "NoQuestions.md", "---\nstatus: next\n---\n# Plain\n\n"+bodySentinel+"\n")
	return vaultDir
}

// warnings captures warnf calls.
type warnings struct {
	mu    sync.Mutex
	lines []string
}

func (w *warnings) warnf(format string, args ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lines = append(w.lines, fmt.Sprintf(format, args...))
}

func (w *warnings) joined() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.lines, "\n")
}

var _ = Describe("Body-free index", func() {
	var (
		ctx      context.Context
		vaultDir string
	)
	BeforeEach(func() {
		ctx = context.Background()
		vaultDir = buildBodyVault()
	})

	It("returns every listed page with empty Content", func() {
		index := newEquivalenceIndex()

		pages, err := index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(indexedNames(pages)).To(Equal([]string{"NoQuestions", "WithQuestions"}))
		for _, page := range pages {
			Expect(string(page.Content)).To(BeEmpty(), page.FileMetadata.Name)
			Expect(page.FrontmatterMap.GetString("status")).NotTo(BeEmpty())
		}

		entries, err := index.ListEntries(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(2))
		for _, entry := range entries {
			Expect(string(entry.Page.Content)).To(BeEmpty(), entry.Page.FileMetadata.Name)
		}
	})

	It("serves open questions from the index entries", func() {
		entries, err := newEquivalenceIndex().ListEntries(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())

		byName := map[string][]domain.OpenQuestion{}
		for _, entry := range entries {
			byName[entry.Page.FileMetadata.Name] = entry.OpenQuestions
		}
		Expect(byName["WithQuestions"]).To(Equal([]domain.OpenQuestion{
			{Index: 1, Text: "Which vault?"},
			{Index: 2, Text: "Answered already"},
		}))
		Expect(byName["NoQuestions"]).NotTo(BeNil())
		Expect(byName["NoQuestions"]).To(BeEmpty())
	})

	It("reads the full body lazily on ReadPage", func() {
		index := newEquivalenceIndex()
		page, err := index.ReadPage(ctx, vaultDir, equivalenceFolder, "WithQuestions")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(page.Content)).To(ContainSubstring(bodySentinel))

		pages, err := index.ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		for _, listed := range pages {
			Expect(string(listed.Content)).To(BeEmpty())
		}
	})
})

var _ = Describe("Body-free store", func() {
	var (
		ctx       context.Context
		storePath string
		warned    *warnings
	)
	BeforeEach(func() {
		ctx = context.Background()
		storePath = filepath.Join(GinkgoT().TempDir(), "vault-ui", "page-index.bolt")
		warned = &warnings{}
	})

	openStore := func() pageindex.Store {
		return pageindex.NewBoltStore(
			ctx, storePath, pageindex.CurrentWriterIdentity(ctx), warned.warnf, warned.warnf,
		)
	}

	It("writes no page body to the store file", func() {
		vaultDir := buildBodyVault()
		store := openStore()
		index := pageindex.NewPageIndexWithStore(
			pageindex.NewPageReader(storage.NewPageStorage(nil)),
			pageindex.NewDirectoryLister(),
			libtime.NewCurrentDateTime(),
			libtime.NewWaiterDuration(),
			store,
		)
		Expect(index.Build(ctx, []pageindex.Key{
			pageindex.NewKey(vaultDir, equivalenceFolder),
		})).To(Succeed())
		Expect(store.Close()).To(Succeed())

		raw, err := os.ReadFile(storePath)
		Expect(err).NotTo(HaveOccurred())
		// Positive control: the store did persist the pages.
		Expect(string(raw)).To(ContainSubstring("WithQuestions"))
		Expect(string(raw)).To(ContainSubstring("Which vault?"))
		Expect(string(raw)).NotTo(ContainSubstring(bodySentinel))
	})

	It("discards a pre-upgrade store holding bodies and logs the format mismatch", func() {
		// The pre-upgrade layout, written with literal names and bytes: format 1
		// identity plus one entry bucket whose record holds a body.
		entryBucket := []byte("vault-ui-page-index\x00/vault\x0024 Tasks")
		Expect(os.MkdirAll(filepath.Dir(storePath), 0700)).To(Succeed())
		db, err := bolt.Open(storePath, 0600, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(db.Update(func(tx *bolt.Tx) error {
			meta, err := tx.CreateBucket([]byte("vault-ui-page-index-meta"))
			if err != nil {
				return err
			}
			if err := meta.Put(
				[]byte("identity"),
				[]byte(`{"StoreFormat":1,"ParserVersion":"v0.171.0"}`),
			); err != nil {
				return err
			}
			entries, err := tx.CreateBucket(entryBucket)
			if err != nil {
				return err
			}
			return entries.Put([]byte("Old.md"), []byte(bodySentinel))
		})).To(Succeed())
		Expect(db.Close()).To(Succeed())

		store := openStore()
		Expect(store.Close()).To(Succeed())

		Expect(warned.joined()).To(ContainSubstring("store format mismatch"))
		Expect(warned.joined()).To(ContainSubstring(storePath))

		db, err = bolt.Open(storePath, 0600, nil)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = db.Close() }()
		Expect(db.View(func(tx *bolt.Tx) error {
			Expect(tx.Bucket(entryBucket)).To(BeNil())
			return nil
		})).To(Succeed())
	})

	It("keeps the store at the vault-ui cache path", func() {
		path, err := pageindex.DefaultStorePath()
		Expect(err).NotTo(HaveOccurred())
		Expect(path).To(HaveSuffix(filepath.Join("vault-ui", "page-index.bolt")))
	})
})
