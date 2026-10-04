// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package statuscache_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/statuscache"
)

// writeTask writes a markdown file with the given frontmatter body under the
// vault's "24 Tasks" folder and returns its path.
func writeTask(vaultPath, name, frontmatter string) string {
	tasksDir := filepath.Join(vaultPath, "24 Tasks")
	path := filepath.Join(tasksDir, name+".md")
	ExpectWithOffset(1, os.WriteFile(path, []byte("---\n"+frontmatter+"\n---\n\nbody\n"), 0o600)).To(Succeed())
	return path
}

var _ = Describe("StatusCache", func() {
	DescribeTable("StatusCache",
		func(body func(vaultPath string)) {
			vaultPath := GinkgoT().TempDir()
			Expect(os.MkdirAll(filepath.Join(vaultPath, "24 Tasks"), 0o750)).To(Succeed())
			body(vaultPath)
		},
		Entry("yaml-true-normalised", func(vaultPath string) {
			writeTask(vaultPath, "Bool Task", "status: in_progress\nclaude_session_started: true")

			cache := statuscache.NewCache()
			Expect(cache.LoadVault("personal", vaultPath, "24 Tasks")).To(Succeed())

			started, ok := cache.GetSessionStarted("personal", "Bool Task")
			Expect(ok).To(BeTrue())
			Expect(started).To(Equal("true"))
		}),
		Entry("status-field-removed-drops-item", func(vaultPath string) {
			path := writeTask(vaultPath, "T", "status: in_progress")

			cache := statuscache.NewCache()
			Expect(cache.LoadVault("personal", vaultPath, "24 Tasks")).To(Succeed())
			Expect(cache.Count("personal")).To(Equal(1))

			Expect(os.WriteFile(path, []byte("---\ntitle: T\n---\n\nbody\n"), 0o600)).To(Succeed())
			cache.Invalidate("personal", "T")

			_, ok := cache.GetStatus("personal", "T")
			Expect(ok).To(BeFalse())
			Expect(cache.Count("personal")).To(Equal(0))
		}),
	)

	It("loads status and marker from the discovered folders", func() {
		vaultPath := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(vaultPath, "24 Tasks"), 0o750)).To(Succeed())
		writeTask(vaultPath, "Starting Task", `status: in_progress`+"\n"+`claude_session_started: "true"`)
		writeTask(vaultPath, "Plain Task", "status: in_progress")

		cache := statuscache.NewCache()
		Expect(cache.LoadVault("personal", vaultPath, "24 Tasks")).To(Succeed())

		status, ok := cache.GetStatus("personal", "Starting Task")
		Expect(ok).To(BeTrue())
		Expect(status).To(Equal("in_progress"))

		started, ok := cache.GetSessionStarted("personal", "Starting Task")
		Expect(ok).To(BeTrue())
		Expect(started).To(Equal("true"))

		_, ok = cache.GetSessionStarted("personal", "Plain Task")
		Expect(ok).To(BeFalse())
	})

	It("preserves an ISO-8601 marker verbatim", func() {
		vaultPath := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(vaultPath, "24 Tasks"), 0o750)).To(Succeed())
		writeTask(vaultPath, "T", "status: in_progress\nclaude_session_started: \"2026-08-29T10:00:00Z\"")

		cache := statuscache.NewCache()
		Expect(cache.LoadVault("personal", vaultPath, "24 Tasks")).To(Succeed())

		started, ok := cache.GetSessionStarted("personal", "T")
		Expect(ok).To(BeTrue())
		Expect(started).To(Equal("2026-08-29T10:00:00Z"))
	})

	It("sets then clears the marker on invalidate", func() {
		vaultPath := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(vaultPath, "24 Tasks"), 0o750)).To(Succeed())
		path := writeTask(vaultPath, "T", "status: in_progress")

		cache := statuscache.NewCache()
		Expect(cache.LoadVault("personal", vaultPath, "24 Tasks")).To(Succeed())
		_, ok := cache.GetSessionStarted("personal", "T")
		Expect(ok).To(BeFalse())

		Expect(os.WriteFile(path, []byte("---\nstatus: in_progress\nclaude_session_started: \"true\"\n---\n\nbody\n"), 0o600)).To(Succeed())
		cache.Invalidate("personal", "T")
		started, ok := cache.GetSessionStarted("personal", "T")
		Expect(ok).To(BeTrue())
		Expect(started).To(Equal("true"))

		Expect(os.WriteFile(path, []byte("---\nstatus: in_progress\n---\n\nbody\n"), 0o600)).To(Succeed())
		cache.Invalidate("personal", "T")
		_, ok = cache.GetSessionStarted("personal", "T")
		Expect(ok).To(BeFalse())
	})

	It("drops an item whose file was deleted on invalidate", func() {
		vaultPath := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(vaultPath, "24 Tasks"), 0o750)).To(Succeed())
		path := writeTask(vaultPath, "T", "status: in_progress\nclaude_session_started: true")

		cache := statuscache.NewCache()
		Expect(cache.LoadVault("personal", vaultPath, "24 Tasks")).To(Succeed())

		Expect(os.Remove(path)).To(Succeed())
		cache.Invalidate("personal", "T")

		_, ok := cache.GetStatus("personal", "T")
		Expect(ok).To(BeFalse())
		_, ok = cache.GetSessionStarted("personal", "T")
		Expect(ok).To(BeFalse())
	})

	It("returns not-found for an unknown vault or item", func() {
		cache := statuscache.NewCache()

		_, ok := cache.GetStatus("nope", "nope")
		Expect(ok).To(BeFalse())
		_, ok = cache.GetSessionStarted("nope", "nope")
		Expect(ok).To(BeFalse())
		Expect(cache.Count("nope")).To(Equal(0))

		// An invalidate against an unknown vault is a no-op.
		Expect(func() { cache.Invalidate("nope", "nope") }).NotTo(Panic())
	})

	It("contributes nothing for a malformed or unreadable file", func() {
		vaultPath := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(vaultPath, "24 Tasks"), 0o750)).To(Succeed())
		// No frontmatter block.
		Expect(os.WriteFile(filepath.Join(vaultPath, "24 Tasks", "no-front.md"), []byte("just body\n"), 0o600)).To(Succeed())
		// Frontmatter that is not a mapping.
		Expect(os.WriteFile(filepath.Join(vaultPath, "24 Tasks", "scalar.md"), []byte("---\njust a scalar\n---\n\nbody\n"), 0o600)).To(Succeed())

		cache := statuscache.NewCache()
		Expect(cache.LoadVault("personal", vaultPath, "24 Tasks")).To(Succeed())
		Expect(cache.Count("personal")).To(Equal(0))
	})

	It("discovers items in nested subfolders", func() {
		vaultPath := GinkgoT().TempDir()
		nested := filepath.Join(vaultPath, "24 Tasks", "nested")
		Expect(os.MkdirAll(nested, 0o750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(nested, "Deep.md"), []byte("---\nstatus: done\n---\n\nbody\n"), 0o600)).To(Succeed())

		cache := statuscache.NewCache()
		Expect(cache.LoadVault("personal", vaultPath, "24 Tasks")).To(Succeed())

		status, ok := cache.GetStatus("personal", "Deep")
		Expect(ok).To(BeTrue())
		Expect(status).To(Equal("done"))
	})
})
