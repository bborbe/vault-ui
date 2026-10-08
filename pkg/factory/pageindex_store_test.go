// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"

	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/pageindex"
)

// cacheDirEnv points the user cache directory at dir so the real cache is never
// touched. linux reads XDG_CACHE_HOME, darwin reads HOME.
func cacheDirEnv(dir string) {
	GinkgoT().Setenv("XDG_CACHE_HOME", dir)
	GinkgoT().Setenv("HOME", dir)
}

var _ = Describe("Page index store wiring", func() {
	It("opens the store at the fixed user-cache path", func() {
		cacheDir := GinkgoT().TempDir()
		cacheDirEnv(cacheDir)

		expected, err := pageindex.DefaultStorePath()
		Expect(err).NotTo(HaveOccurred())

		store := factory.CreatePageIndexStore(context.Background())
		DeferCleanup(func() { _ = store.Close() })

		Expect(store.Path()).To(Equal(expected))
		Expect(filepath.Base(store.Path())).To(Equal("page-index.bolt"))

		info, err := os.Stat(filepath.Dir(store.Path()))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.IsDir()).To(BeTrue())
	})

	It("serves the same data as a no-store index", func() {
		vault := newIndexVault("alpha")
		cacheDir := GinkgoT().TempDir()
		cacheDirEnv(cacheDir)

		seams := newCountingSeams()
		store := factory.CreatePageIndexStore(context.Background())
		DeferCleanup(func() { _ = store.Close() })
		pageIndex := factory.CreatePageIndexWithStore(
			seams, seams, libtime.NewCurrentDateTime(), store,
		)

		got, err := pageIndex.ListPages(context.Background(), vault.path, vault.tasks)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(BeEmpty())

		plainSeams := newCountingSeams()
		plain := factory.CreatePageIndex(
			plainSeams, plainSeams, libtime.NewCurrentDateTime(),
		)
		want, err := plain.ListPages(context.Background(), vault.path, vault.tasks)
		Expect(err).NotTo(HaveOccurred())

		Expect(reflect.DeepEqual(got, want)).To(BeTrue())
	})

	It("never creates a .bolt file under a vault", func() {
		vault := newIndexVault("alpha")
		cacheDir := GinkgoT().TempDir()
		cacheDirEnv(cacheDir)

		seams := newCountingSeams()
		store := factory.CreatePageIndexStore(context.Background())
		DeferCleanup(func() { _ = store.Close() })
		pageIndex := factory.CreatePageIndexWithStore(
			seams, seams, libtime.NewCurrentDateTime(), store,
		)

		_, err := pageIndex.ListPages(context.Background(), vault.path, vault.tasks)
		Expect(err).NotTo(HaveOccurred())

		Expect(filepath.Walk(vault.path, func(path string, info os.FileInfo, err error) error {
			Expect(err).NotTo(HaveOccurred())
			if !info.IsDir() {
				Expect(filepath.Ext(path)).NotTo(Equal(".bolt"))
			}
			return nil
		})).To(Succeed())
	})
})
