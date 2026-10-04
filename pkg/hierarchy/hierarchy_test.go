// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package hierarchy_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/hierarchy"
)

// names returns the base names of the discovered folder paths.
func names(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		result = append(result, filepath.Base(path))
	}
	return result
}

// mkdirs creates each named directory under root and returns root.
func mkdirs(root string, dirs ...string) string {
	for _, dir := range dirs {
		ExpectWithOffset(1, os.MkdirAll(filepath.Join(root, dir), 0o750)).To(Succeed())
	}
	return root
}

var _ = Describe("HierarchyFolders", func() {
	DescribeTable("HierarchyFolders",
		func(body func(vaultPath string)) {
			body(GinkgoT().TempDir())
		},
		Entry("suffix-and-numeric-order", func(vaultPath string) {
			mkdirs(vaultPath,
				"21 Themes",
				"22 Objectives",
				"23 Goals",
				"24 Tasks",
				"40 Tasks",
				"50 Knowledge",
				"random",
			)

			found, err := hierarchy.DiscoverHierarchyFolders(vaultPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(names(found)).To(Equal([]string{
				"21 Themes",
				"22 Objectives",
				"23 Goals",
				"24 Tasks",
				"40 Tasks",
			}))
		}),
	)

	It("returns an empty slice for a missing vault path", func() {
		found, err := hierarchy.DiscoverHierarchyFolders(filepath.Join(GinkgoT().TempDir(), "does-not-exist"))
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeEmpty())
	})

	It("orders folders without a numeric prefix by category then name", func() {
		vaultPath := mkdirs(GinkgoT().TempDir(), "Tasks", "Goals", "Themes", "Objectives")

		found, err := hierarchy.DiscoverHierarchyFolders(vaultPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(names(found)).To(Equal([]string{"Themes", "Objectives", "Goals", "Tasks"}))
	})

	It("prefers the configured tasks folder and excludes other Tasks folders", func() {
		vaultPath := mkdirs(GinkgoT().TempDir(), "21 Themes", "24 Tasks", "40 Tasks")

		found, err := hierarchy.DiscoverHierarchyFoldersForVault(vaultPath, "24 Tasks")
		Expect(err).NotTo(HaveOccurred())
		Expect(names(found)).To(Equal([]string{"21 Themes", "24 Tasks"}))
	})

	It("returns the discovered set unchanged when the configured tasks folder is absent", func() {
		vaultPath := mkdirs(GinkgoT().TempDir(), "21 Themes", "40 Tasks")

		found, err := hierarchy.DiscoverHierarchyFoldersForVault(vaultPath, "24 Tasks")
		Expect(err).NotTo(HaveOccurred())
		Expect(names(found)).To(Equal([]string{"21 Themes", "40 Tasks"}))
	})

	It("orders non-numeric prefixes last within their category", func() {
		vaultPath := mkdirs(GinkgoT().TempDir(), "Themes", "21 Themes")

		found, err := hierarchy.DiscoverHierarchyFolders(vaultPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(names(found)).To(Equal([]string{"21 Themes", "Themes"}))
	})
})
