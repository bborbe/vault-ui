// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/pageindex"
)

// vlogSink captures the store's V(2) lines.
type vlogSink struct {
	mu    sync.Mutex
	lines []string
}

func (v *vlogSink) vlogf(format string, args ...any) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.lines = append(v.lines, fmt.Sprintf(format, args...))
}

func (v *vlogSink) all() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.lines...)
}

// currentIdentity is this process's writer identity.
func currentIdentity() pageindex.WriterIdentity {
	return pageindex.CurrentWriterIdentity(context.Background())
}

// newStoreAt opens a store at an explicit path with explicit seams.
func newStoreAt(
	path string,
	identity pageindex.WriterIdentity,
	warns *warnSink,
	vlogs *vlogSink,
) pageindex.Store {
	return pageindex.NewBoltStore(
		context.Background(), path, identity, warns.warnf, vlogs.vlogf,
	)
}

// newStore opens a store at path with this process's identity.
func newStore(path string, warns *warnSink, vlogs *vlogSink) pageindex.Store {
	return newStoreAt(path, currentIdentity(), warns, vlogs)
}

// closeStore closes a store, ignoring the close error in test cleanup.
func closeStore(store pageindex.Store) {
	_ = store.Close()
}

// readFixtureEntries reads every .md entry the production lister reports,
// returning the entries to store and the fingerprint each read reported.
func readFixtureEntries(
	ctx context.Context,
	vaultDir string,
) ([]pageindex.StoredEntry, map[string]pageindex.FileFingerprint) {
	lister := pageindex.NewDirectoryLister()
	entries, err := lister.ListFiles(ctx, vaultDir, equivalenceFolder)
	Expect(err).NotTo(HaveOccurred())
	Expect(entries).NotTo(BeEmpty())

	reader := pageindex.NewPageReader(storage.NewPageStorage(nil))
	puts := make([]pageindex.StoredEntry, 0, len(entries))
	fingerprints := make(map[string]pageindex.FileFingerprint, len(entries))
	for _, entry := range entries {
		page, fingerprint, readErr := reader.ReadPage(
			ctx, vaultDir, equivalenceFolder, entry.Name,
		)
		fingerprints[entry.Name] = fingerprint
		if readErr != nil {
			puts = append(puts, pageindex.StoredEntry{
				Filename:    entry.Name,
				Fingerprint: fingerprint,
			})
			continue
		}
		puts = append(puts, pageindex.StoredEntry{
			Filename:    entry.Name,
			Page:        page,
			Fingerprint: fingerprint,
		})
	}
	return puts, fingerprints
}

// simpleEntry builds a stored entry with a minimal page.
func simpleEntry(name, title string) pageindex.StoredEntry {
	return pageindex.StoredEntry{
		Filename:    name,
		Page:        domain.NewPage(map[string]any{"title": title}, domain.FileMetadata{Name: strings.TrimSuffix(name, ".md")}, domain.Content("# "+title+"\n")),
		Fingerprint: pageindex.FileFingerprint{Size: int64(len(name))},
	}
}

var _ = Describe("Page index store", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("codec", func() {
		It("AC1 round-trips every awkward file losslessly", func() {
			vaultDir := buildEquivalenceVault()
			key := pageindex.NewKey(vaultDir, equivalenceFolder)
			puts, fingerprints := readFixtureEntries(ctx, vaultDir)

			warns := &warnSink{}
			vlogs := &vlogSink{}
			store := newStore(filepath.Join(GinkgoT().TempDir(), "page-index.bolt"), warns, vlogs)
			defer closeStore(store)

			Expect(store.Write(ctx, key, puts, nil)).To(Succeed())
			loaded, ok, err := store.Load(ctx, key)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(loaded).To(HaveLen(len(puts)))

			gotByName := make(map[string]pageindex.StoredEntry, len(loaded))
			pageNames := make([]string, 0, len(loaded))
			for _, entry := range loaded {
				gotByName[entry.Filename] = entry
				if entry.Page != nil {
					pageNames = append(pageNames, entry.Filename)
				}
			}
			// Positive control: a fixture that silently excluded everything
			// could not produce this ordered non-empty list.
			Expect(pageNames).To(Equal(
				[]string{"Inside.md", "Plain.md", "Wikilink.md", "Ünïcode.md"},
			))

			reference := storage.NewPageStorage(nil)
			for _, entry := range puts {
				got, found := gotByName[entry.Filename]
				Expect(found).To(BeTrue(), "missing stored entry for %s", entry.Filename)
				Expect(got.Fingerprint).To(Equal(fingerprints[entry.Filename]))

				want, readErr := reference.ReadPage(
					ctx, vaultDir, equivalenceFolder, strings.TrimSuffix(entry.Filename, ".md"),
				)
				if readErr != nil {
					Expect(got.Page).To(BeNil(), "excluded %s must store no page", entry.Filename)
					continue
				}
				Expect(got.Page).NotTo(BeNil(), "readable %s must store a page", entry.Filename)
				Expect(reflect.DeepEqual(got.Page, want)).To(
					BeTrue(), "%s page is not losslessly round-tripped", entry.Filename,
				)
			}
		})

		It("converts a DateOrDateTime frontmatter value to time.Time", func() {
			key := pageindex.NewKey("/vault-a", "24 Tasks")
			path := filepath.Join(GinkgoT().TempDir(), "page-index.bolt")
			store := newStore(path, &warnSink{}, &vlogSink{})
			defer closeStore(store)

			date := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			page := domain.NewPage(map[string]any{
				"defer_date": libtime.DateOrDateTime(date),
				"nested":     map[string]any{"due_date": libtime.DateOrDateTime(date)},
				"list":       []any{"a", libtime.DateOrDateTime(date)},
				"generic":    map[any]any{"k": "v"},
				"title":      "Dates",
			}, domain.FileMetadata{Name: "Dates"}, domain.Content("# Dates\n"))
			Expect(store.Write(ctx, key, []pageindex.StoredEntry{
				{Filename: "Dates.md", Page: page},
			}, nil)).To(Succeed())

			loaded, ok, err := store.Load(ctx, key)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(loaded).To(HaveLen(1))

			want := domain.NewPage(map[string]any{
				"defer_date": date,
				"nested":     map[string]any{"due_date": date},
				"list":       []any{"a", date},
				"generic":    map[any]any{"k": "v"},
				"title":      "Dates",
			}, domain.FileMetadata{Name: "Dates"}, domain.Content("# Dates\n"))
			Expect(reflect.DeepEqual(loaded[0].Page, want)).To(
				BeTrue(), "DateOrDateTime was not converted to time.Time",
			)
		})
	})

	Describe("discard on mismatch", func() {
		keyA := pageindex.NewKey("/vault-a", "24 Tasks")
		keyB := pageindex.NewKey("/vault-b", "24 Tasks")

		It("AC4(a) treats a store file that never existed as absent", func() {
			warns := &warnSink{}
			path := filepath.Join(GinkgoT().TempDir(), "page-index.bolt")
			store := newStore(path, warns, &vlogSink{})
			defer closeStore(store)

			entries, ok, err := store.Load(ctx, keyA)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
			Expect(entries).To(BeNil())
			Expect(warns.naming("store file missing or empty")).To(HaveLen(1))
			Expect(warns.naming(path)).To(HaveLen(1))
		})

		It("AC4(b) opens a zero-byte store file empty without panicking", func() {
			warns := &warnSink{}
			path := filepath.Join(GinkgoT().TempDir(), "page-index.bolt")
			Expect(os.WriteFile(path, nil, 0600)).To(Succeed())

			store := newStore(path, warns, &vlogSink{})
			defer closeStore(store)

			_, ok, err := store.Load(ctx, keyA)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
			Expect(warns.naming("store file missing or empty")).To(HaveLen(1))
		})

		It("AC4(c) discards a store file holding garbage bytes", func() {
			warns := &warnSink{}
			path := filepath.Join(GinkgoT().TempDir(), "page-index.bolt")
			Expect(os.WriteFile(path, []byte("not a bolt database at all"), 0600)).To(Succeed())

			store := newStore(path, warns, &vlogSink{})
			defer closeStore(store)
			// A discarded store warns only from Load, never from the open.
			Expect(warns.naming("open store")).To(BeEmpty())

			_, ok, err := store.Load(ctx, keyA)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
			Expect(warns.naming("open store")).To(HaveLen(1))
			Expect(warns.naming(path)).To(HaveLen(1))
			Expect(store.Path()).To(Equal(path))
			Expect(store.Write(ctx, keyA, nil, nil)).To(HaveOccurred())
		})

		It("discards a store whose directory cannot be created", func() {
			blocker := filepath.Join(GinkgoT().TempDir(), "blocker")
			Expect(os.WriteFile(blocker, []byte("x"), 0600)).To(Succeed())
			path := filepath.Join(blocker, "nested", "page-index.bolt")

			warns := &warnSink{}
			store := newStore(path, warns, &vlogSink{})
			defer closeStore(store)

			_, ok, err := store.Load(ctx, keyA)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
			Expect(warns.naming("create store directory")).To(HaveLen(1))
		})

		It("AC4(d) discards a store file whose read permission was removed", func() {
			if os.Geteuid() == 0 {
				GinkgoT().Skip("a root process can still read a 0000 file")
			}
			warns := &warnSink{}
			path := filepath.Join(GinkgoT().TempDir(), "page-index.bolt")
			valid := newStore(path, &warnSink{}, &vlogSink{})
			Expect(valid.Write(ctx, keyA, []pageindex.StoredEntry{simpleEntry("A.md", "A")}, nil)).
				To(Succeed())
			Expect(valid.Close()).To(Succeed())
			Expect(os.Chmod(path, 0)).To(Succeed())
			defer func() { _ = os.Chmod(path, 0600) }()

			store := newStore(path, warns, &vlogSink{})
			defer closeStore(store)

			_, ok, err := store.Load(ctx, keyA)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
			Expect(warns.naming(path)).To(HaveLen(1))
		})

		It("AC4(e) discards a store written under a different store format", func() {
			warns := &warnSink{}
			path := filepath.Join(GinkgoT().TempDir(), "page-index.bolt")

			first := newStoreAt(
				path,
				pageindex.WriterIdentity{StoreFormat: 1, ParserVersion: "x"},
				&warnSink{}, &vlogSink{},
			)
			Expect(first.Write(ctx, keyA, []pageindex.StoredEntry{simpleEntry("A.md", "A")}, nil)).
				To(Succeed())
			Expect(first.Close()).To(Succeed())

			store := newStoreAt(
				path,
				pageindex.WriterIdentity{StoreFormat: 2, ParserVersion: "x"},
				warns, &vlogSink{},
			)
			defer closeStore(store)

			_, ok, err := store.Load(ctx, keyA)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
			Expect(warns.naming("writer identity mismatch")).To(HaveLen(1))
			Expect(warns.naming(path)).To(HaveLen(1))

			// The mismatch wipes and takes the full parse; the store is usable.
			Expect(store.Write(ctx, keyA, []pageindex.StoredEntry{simpleEntry("B.md", "B")}, nil)).
				To(Succeed())
			loaded, ok, err := store.Load(ctx, keyA)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(loaded).To(HaveLen(1))
			Expect(loaded[0].Filename).To(Equal("B.md"))
		})

		It("AC4(f) discards a store written by a different parser version", func() {
			warns := &warnSink{}
			path := filepath.Join(GinkgoT().TempDir(), "page-index.bolt")

			first := newStoreAt(
				path,
				pageindex.WriterIdentity{StoreFormat: 1, ParserVersion: "v0.1.0"},
				&warnSink{}, &vlogSink{},
			)
			Expect(first.Write(ctx, keyA, []pageindex.StoredEntry{simpleEntry("A.md", "A")}, nil)).
				To(Succeed())
			Expect(first.Close()).To(Succeed())

			store := newStoreAt(
				path,
				pageindex.WriterIdentity{StoreFormat: 1, ParserVersion: "v0.2.0"},
				warns, &vlogSink{},
			)
			defer closeStore(store)

			_, ok, err := store.Load(ctx, keyA)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
			Expect(warns.naming("writer identity mismatch")).To(HaveLen(1))
		})

		It("AC4(g) serves one key while another full-parses", func() {
			warns := &warnSink{}
			path := filepath.Join(GinkgoT().TempDir(), "page-index.bolt")
			store := newStore(path, warns, &vlogSink{})
			defer closeStore(store)

			Expect(store.Write(ctx, keyA, []pageindex.StoredEntry{simpleEntry("A.md", "A")}, nil)).
				To(Succeed())

			loaded, ok, err := store.Load(ctx, keyA)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(loaded).To(HaveLen(1))
			Expect(warns.naming("no stored entries for key")).To(BeEmpty())

			entries, ok, err := store.Load(ctx, keyB)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
			Expect(entries).To(BeNil())
			missing := warns.naming("no stored entries for key")
			Expect(missing).To(HaveLen(1))
			Expect(missing[0]).To(ContainSubstring(path))
		})
	})

	Describe("write", func() {
		var (
			key   pageindex.Key
			path  string
			warns *warnSink
			store pageindex.Store
		)

		BeforeEach(func() {
			key = pageindex.NewKey("/vault-a", "24 Tasks")
			path = filepath.Join(GinkgoT().TempDir(), "page-index.bolt")
			warns = &warnSink{}
			store = newStore(path, warns, &vlogSink{})
		})

		AfterEach(func() {
			closeStore(store)
		})

		It("AC5 puts and deletes the delta in one transaction", func() {
			Expect(store.Path()).To(Equal(path))
			Expect(store.Write(ctx, key, []pageindex.StoredEntry{
				simpleEntry("A.md", "A"),
				simpleEntry("B.md", "B"),
			}, nil)).To(Succeed())
			loaded, ok, err := store.Load(ctx, key)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(loaded).To(HaveLen(2))

			Expect(store.Write(ctx, key, nil, []string{"A.md"})).To(Succeed())
			loaded, ok, err = store.Load(ctx, key)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(loaded).To(HaveLen(1))
			Expect(loaded[0].Filename).To(Equal("B.md"))
		})

		It("AC5 leaves the previous content intact when a write fails", func() {
			Expect(store.Write(ctx, key, []pageindex.StoredEntry{simpleEntry("A.md", "A")}, nil)).
				To(Succeed())

			broken := pageindex.StoredEntry{
				Filename: "Broken.md",
				Page: domain.NewPage(
					map[string]any{"bad": make(chan int)},
					domain.FileMetadata{Name: "Broken"},
					domain.Content("# Broken\n"),
				),
			}
			Expect(store.Write(ctx, key, []pageindex.StoredEntry{broken}, nil)).
				To(HaveOccurred())

			loaded, ok, err := store.Load(ctx, key)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(loaded).To(HaveLen(1))
			Expect(loaded[0].Filename).To(Equal("A.md"))
		})

		It("AC5 makes a written delta observable through a second store", func() {
			Expect(store.Write(ctx, key, []pageindex.StoredEntry{simpleEntry("A.md", "A")}, nil)).
				To(Succeed())
			Expect(store.Close()).To(Succeed())

			secondWarns := &warnSink{}
			second := newStore(path, secondWarns, &vlogSink{})
			defer closeStore(second)

			loaded, ok, err := second.Load(ctx, key)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(loaded).To(HaveLen(1))
			Expect(loaded[0].Filename).To(Equal("A.md"))
		})
	})

	Describe("location", func() {
		It("AC6 resolves the fixed cache path", func() {
			cacheDir, err := os.UserCacheDir()
			Expect(err).NotTo(HaveOccurred())

			path, err := pageindex.DefaultStorePath()
			Expect(err).NotTo(HaveOccurred())
			Expect(path).To(Equal(filepath.Join(cacheDir, "vault-ui", "page-index.bolt")))
		})

		It("AC6 opens the default store at the cache path", func() {
			cacheDir := GinkgoT().TempDir()
			GinkgoT().Setenv("XDG_CACHE_HOME", cacheDir)

			key := pageindex.NewKey("/vault-a", "24 Tasks")
			warns := &warnSink{}
			store := pageindex.OpenDefaultStore(ctx, warns.warnf, (&vlogSink{}).vlogf)
			defer closeStore(store)

			Expect(store.Path()).To(Equal(
				filepath.Join(cacheDir, "vault-ui", "page-index.bolt"),
			))
			Expect(store.Write(ctx, key, []pageindex.StoredEntry{simpleEntry("A.md", "A")}, nil)).
				To(Succeed())
			loaded, ok, err := store.Load(ctx, key)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(loaded).To(HaveLen(1))
		})

		It("AC6 creates a missing store directory", func() {
			path := filepath.Join(GinkgoT().TempDir(), "missing", "page-index.bolt")
			store := newStore(path, &warnSink{}, &vlogSink{})
			defer closeStore(store)

			info, err := os.Stat(filepath.Dir(path))
			Expect(err).NotTo(HaveOccurred())
			Expect(info.IsDir()).To(BeTrue())
			_, err = os.Stat(path)
			Expect(err).NotTo(HaveOccurred())
		})

		It("AC6 never writes the store under a vault directory", func() {
			vaultDir := buildEquivalenceVault()
			key := pageindex.NewKey(vaultDir, equivalenceFolder)
			puts, _ := readFixtureEntries(ctx, vaultDir)

			path := filepath.Join(GinkgoT().TempDir(), "page-index.bolt")
			store := newStore(path, &warnSink{}, &vlogSink{})
			defer closeStore(store)
			Expect(store.Write(ctx, key, puts, nil)).To(Succeed())

			var bolts []string
			Expect(filepath.WalkDir(vaultDir, func(p string, _ os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if strings.HasSuffix(p, ".bolt") {
					bolts = append(bolts, p)
				}
				return nil
			})).To(Succeed())
			Expect(bolts).To(BeEmpty())
		})
	})

	Describe("reporting", func() {
		It("AC7 warns nothing on a hit and reports keys and entries at V(2)", func() {
			key := pageindex.NewKey("/vault-a", "24 Tasks")
			path := filepath.Join(GinkgoT().TempDir(), "page-index.bolt")

			first := newStore(path, &warnSink{}, &vlogSink{})
			Expect(first.Write(ctx, key, []pageindex.StoredEntry{
				simpleEntry("A.md", "A"),
				simpleEntry("B.md", "B"),
			}, nil)).To(Succeed())
			Expect(first.Close()).To(Succeed())

			warns := &warnSink{}
			vlogs := &vlogSink{}
			store := newStore(path, warns, vlogs)
			defer closeStore(store)

			Expect(vlogs.all()).To(HaveLen(1))
			Expect(vlogs.all()[0]).To(ContainSubstring(path))
			Expect(vlogs.all()[0]).To(ContainSubstring("1 keys"))
			Expect(vlogs.all()[0]).To(ContainSubstring("2 entries"))

			loaded, ok, err := store.Load(ctx, key)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(loaded).To(HaveLen(2))
			Expect(warns.naming("")).To(BeEmpty())
		})
	})

	Describe("bounded open", func() {
		It("AC-safety times out instead of hanging on a locked store", NodeTimeout(30*time.Second), func(specCtx SpecContext) {
			ctx = specCtx
			key := pageindex.NewKey("/vault-a", "24 Tasks")
			path := filepath.Join(GinkgoT().TempDir(), "page-index.bolt")

			first := newStore(path, &warnSink{}, &vlogSink{})
			defer closeStore(first)
			Expect(first.Write(ctx, key, []pageindex.StoredEntry{simpleEntry("A.md", "A")}, nil)).
				To(Succeed())

			warns := &warnSink{}
			var second pageindex.Store
			done := make(chan struct{})
			start := time.Now()
			go func() {
				defer GinkgoRecover()
				defer close(done)
				second = newStore(path, warns, &vlogSink{})
			}()

			Eventually(done, "20s").Should(BeClosed())
			Expect(time.Since(start)).To(BeNumerically("<", 15*time.Second))
			Expect(second).NotTo(BeNil())

			entries, ok, err := second.Load(ctx, key)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
			Expect(entries).To(BeNil())
			Expect(warns.naming("open store")).To(HaveLen(1))
		})
	})
})
