// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/pageindex"
)

// equivalenceFolder is the fixture's vault-relative pages dir.
const equivalenceFolder = "24 Tasks"

// writeFixtureFile writes one file under the vault's pages folder.
func writeFixtureFile(vaultDir, name, content string) {
	Expect(os.WriteFile(
		filepath.Join(vaultDir, equivalenceFolder, name), []byte(content), 0600,
	)).To(Succeed())
}

// buildEquivalenceVault creates the awkward-files fixture: a plain page, a bare
// wikilink, a file without frontmatter, a file with invalid YAML, a non-.md
// file, a subdirectory, a symlink out of the vault, a broken symlink, a symlink
// to a page inside the vault and a non-ASCII filename.
func buildEquivalenceVault() string {
	vaultDir := GinkgoT().TempDir()
	folderDir := filepath.Join(vaultDir, equivalenceFolder)
	Expect(os.MkdirAll(folderDir, 0750)).To(Succeed())
	Expect(os.MkdirAll(filepath.Join(folderDir, "subdir"), 0750)).To(Succeed())

	writeFixtureFile(vaultDir, "Plain.md", "---\ntitle: Plain\nstatus: next\n---\n# Plain\n")
	writeFixtureFile(
		vaultDir, "Wikilink.md", "---\ntitle: Wikilink\nrelated: [[Other]]\n---\n# Wikilink\n",
	)
	writeFixtureFile(vaultDir, "NoFrontmatter.md", "# No frontmatter\n")
	writeFixtureFile(vaultDir, "BadYaml.md", "---\ntitle: [unclosed\n---\n# Bad\n")
	writeFixtureFile(vaultDir, "notes.txt", "not a page\n")
	writeFixtureFile(vaultDir, "Ünïcode.md", "---\ntitle: Ünïcode\n---\n# Ünïcode\n")

	// The outside target lives in its own temp dir, never under the vault.
	outsideDir := GinkgoT().TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.md")
	Expect(os.WriteFile(outsideFile, []byte("---\ntitle: Secret\n---\n# Secret\n"), 0600)).
		To(Succeed())
	Expect(os.Symlink(outsideFile, filepath.Join(folderDir, "Outside.md"))).To(Succeed())

	// A broken symlink counts as outside.
	Expect(os.Symlink(
		filepath.Join(folderDir, "does-not-exist.md"), filepath.Join(folderDir, "Broken.md"),
	)).To(Succeed())

	// A symlink to a page inside the vault stays in the listing.
	Expect(os.Symlink("Plain.md", filepath.Join(folderDir, "Inside.md"))).To(Succeed())

	return vaultDir
}

// indexedNames returns the pages' file names in listing order.
func indexedNames(pages []*domain.Page) []string {
	names := make([]string, 0, len(pages))
	for _, page := range pages {
		names = append(names, page.FileMetadata.Name)
	}
	return names
}

// newEquivalenceIndex builds the index over the production reader and lister.
func newEquivalenceIndex() pageindex.PageIndex {
	return pageindex.NewPageIndex(
		pageindex.NewPageReader(),
		pageindex.NewDirectoryLister(),
		libtime.NewCurrentDateTime(),
		libtime.NewWaiterDuration(),
	)
}

var _ = Describe("Snapshot equivalence", func() {
	It("matches vault-cli's own folder listing over awkward files", func() {
		ctx := context.Background()
		vaultDir := buildEquivalenceVault()

		want, err := storage.NewPageStorage(nil).ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		// Positive control: a fixture that silently excluded everything would
		// still deep-equal an equally empty snapshot.
		Expect(indexedNames(want)).To(Equal([]string{"Inside", "Plain", "Wikilink", "Ünïcode"}))

		got, err := newEquivalenceIndex().ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())

		Expect(reflect.DeepEqual(got, want)).To(BeTrue())
	})

	It("matches vault-cli on an existing folder with no page files", func() {
		ctx := context.Background()
		vaultDir := GinkgoT().TempDir()
		Expect(os.MkdirAll(
			filepath.Join(vaultDir, equivalenceFolder), 0750,
		)).To(Succeed())

		want, err := storage.NewPageStorage(nil).ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(want).NotTo(BeNil())
		Expect(want).To(BeEmpty())

		got, err := newEquivalenceIndex().ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())

		Expect(reflect.DeepEqual(got, want)).To(BeTrue())
	})

	It("matches vault-cli on a missing folder", func() {
		ctx := context.Background()
		vaultDir := GinkgoT().TempDir()

		want, err := storage.NewPageStorage(nil).ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())
		Expect(want).To(BeNil())

		got, err := newEquivalenceIndex().ListPages(ctx, vaultDir, equivalenceFolder)
		Expect(err).NotTo(HaveOccurred())

		Expect(reflect.DeepEqual(got, want)).To(BeTrue())
	})
})
