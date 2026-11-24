// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageview_test

import (
	"context"
	stderrors "errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	markdown "github.com/bborbe/vault-markdown"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/board"
	"github.com/bborbe/vault-ui/pkg/pageview"
	"github.com/bborbe/vault-ui/pkg/vaultapi"
)

const vaultName = "private-personal"

const fixtureAlpha = `---
title: Alpha
---

# Alpha

| Name | Value |
| --- | --- |
| one | 1 |

> [!note] Important Title
> The callout body.

See [[Beta]].
`

type fixtureVaults struct{ dir string }

func (v fixtureVaults) Vaults(context.Context) ([]board.Vault, error) {
	return []board.Vault{{
		Name:      vaultName,
		VaultName: vaultName,
		Path:      v.dir,
	}}, nil
}

// displayNameVaults separates the config key (Name) from the display name
// (VaultName), which is what a real vault config produces and what the board's
// Obsidian deep links carry.
type displayNameVaults struct{ dir string }

func (v displayNameVaults) Vaults(context.Context) ([]board.Vault, error) {
	return []board.Vault{{
		Name:      "fixture",
		VaultName: "Fixture",
		Path:      v.dir,
	}}, nil
}

// testHelper is the slice of testing.TB GinkgoT implements.
type testHelper interface {
	Helper()
	TempDir() string
	Fatalf(format string, args ...any)
}

// fixtureSource is a PageSource over a fixture vault on disk. It stands in for
// the vault service: a Page read answers the body plus the link and embed maps
// the renderer needs, and a Backlinks read answers the inbound set computed over
// the whole fixture vault.
type fixtureSource struct {
	bodies map[string]string
	byName map[string]string
}

// newFixtureSource walks dir once and records every markdown page by relative
// path and by bare page name.
func newFixtureSource(tb testHelper, dir string) *fixtureSource {
	tb.Helper()
	source := &fixtureSource{
		bodies: map[string]string{},
		byName: map[string]string{},
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		raw, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		source.bodies[rel] = string(raw)
		source.byName[strings.TrimSuffix(d.Name(), ".md")] = rel
		return nil
	})
	if err != nil {
		tb.Fatalf("index fixture vault: %v", err)
	}
	return source
}

func (f *fixtureSource) Page(_ context.Context, _ string, pagePath string) (*vaultapi.Page, error) {
	body, ok := f.bodies[pagePath]
	if !ok {
		return nil, vaultapi.NotFoundError{Message: "page not found: " + pagePath}
	}
	links := make(map[string]string, len(f.byName))
	embeds := make(map[string]string, len(f.byName))
	for name, rel := range f.byName {
		links[name] = rel
		embeds[name] = f.bodies[rel]
	}
	return &vaultapi.Page{
		Vault:   vaultName,
		Path:    pagePath,
		Name:    strings.TrimSuffix(path.Base(pagePath), ".md"),
		Content: body,
		Links:   links,
		Embeds:  embeds,
	}, nil
}

func (f *fixtureSource) Backlinks(
	_ context.Context,
	_ string,
	pagePath string,
) ([]vaultapi.Backlink, error) {
	if _, ok := f.bodies[pagePath]; !ok {
		return nil, vaultapi.NotFoundError{Message: "page not found: " + pagePath}
	}
	name := strings.TrimSuffix(path.Base(pagePath), ".md")
	rels := markdown.Backlinks(f.bodies, name)
	out := make([]vaultapi.Backlink, 0, len(rels))
	for _, rel := range rels {
		out = append(out, vaultapi.Backlink{
			Path: rel,
			Name: strings.TrimSuffix(path.Base(rel), ".md"),
		})
	}
	return out, nil
}

// newFixture writes the shared fixture vault and returns its directory.
func newFixture(tb testHelper) string {
	tb.Helper()
	dir := tb.TempDir()
	files := map[string]string{
		"Alpha.md": fixtureAlpha,
		"Beta.md":  "---\ntitle: Beta\n---\n\n# Beta\n\nBeta body text.\n",
		"Gamma.md": "---\ntitle: Gamma\n---\n\n# Gamma\n\n![[Beta]]\n",
		"Delta.md": "---\ntitle: Delta\n---\n\n# Delta\n\n[[Nope]]\n",
		"Links.md": "---\ntitle: Links\n---\n\n# Links\n\n[[Alpha]]\n",
		"Echo.md":  "---\ntitle: Echo\n---\n\n# Echo\n\n<script>alert(1)</script>\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			tb.Fatalf("write fixture %s: %v", name, err)
		}
	}
	return dir
}

// newService builds the real service over a fixture PageSource.
func newService(tb testHelper, dir string) pageview.Service {
	tb.Helper()
	return pageview.NewService(fixtureVaults{dir: dir}, newFixtureSource(tb, dir))
}

// errorSource fails every page read with a generic (non-not-found) error.
type errorSource struct{ err error }

func (e errorSource) Page(context.Context, string, string) (*vaultapi.Page, error) {
	return nil, e.err
}

func (e errorSource) Backlinks(context.Context, string, string) ([]vaultapi.Backlink, error) {
	return nil, e.err
}

// backlinkErrorSource serves a page but fails the backlink read.
type backlinkErrorSource struct {
	page *vaultapi.Page
	err  error
}

func (b backlinkErrorSource) Page(context.Context, string, string) (*vaultapi.Page, error) {
	return b.page, nil
}

func (b backlinkErrorSource) Backlinks(context.Context, string, string) ([]vaultapi.Backlink, error) {
	return nil, b.err
}

// newFolderFixture writes a fixture vault whose pages live in separate folders,
// so cross-folder resolution and backlinks are exercised without a browser.
func newFolderFixture(tb testHelper) string {
	tb.Helper()
	dir := tb.TempDir()
	files := map[string]string{
		filepath.Join("24 Goals", "Cross Folder Goal.md"):   "---\npage_type: goal\n---\n\n# Goal\n\nSee [[Cross Folder Task]].\n",
		filepath.Join("25 Tasks", "Cross Folder Task.md"):   "---\npage_type: task\n---\n\n# Task\n\nTask body marker.\n",
		filepath.Join("50 Knowledge Base", "Plain Page.md"): "# Plain Page\n\nNo frontmatter body marker.\n",
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			tb.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			tb.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir
}

var _ = Describe("Page view", func() {
	// The card and obsidian:// links both carry the display name, so the
	// resolver must accept it as well as the config key, in any case.
	DescribeTable("resolves the vault by display name or config key",
		func(name string) {
			dir := newFixture(GinkgoT())
			service := pageview.NewService(displayNameVaults{dir: dir}, newFixtureSource(GinkgoT(), dir))
			_, err := service.Render(context.Background(), name, "Alpha.md")
			Expect(err).NotTo(HaveOccurred())
		},
		Entry("config key", "fixture"),
		Entry("display name", "Fixture"),
		Entry("uppercase display name", "FIXTURE"),
	)

	It("renders wikilinks and backlinks", func() {
		service := newService(GinkgoT(), newFixture(GinkgoT()))
		ctx := context.Background()

		alpha, err := service.Render(ctx, vaultName, "Alpha.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(alpha).To(ContainSubstring(`<a class="wikilink" href="/v/private-personal/page/Beta.md">`))
		Expect(alpha).To(ContainSubstring(`href="/v/private-personal/page/Links.md"`))
		Expect(alpha).To(ContainSubstring("<table>"))

		beta, err := service.Render(ctx, vaultName, "Beta.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(beta).To(ContainSubstring(`href="/v/private-personal/page/Alpha.md"`))
		Expect(beta).To(ContainSubstring(`href="/v/private-personal/page/Gamma.md"`))
		Expect(beta).NotTo(ContainSubstring(`href="/v/private-personal/page/Delta.md"`))
	})

	It("inlines an embedded page", func() {
		service := newService(GinkgoT(), newFixture(GinkgoT()))
		gamma, err := service.Render(context.Background(), vaultName, "Gamma.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(gamma).To(ContainSubstring("Beta body text."))
	})

	It("leaves a missing link as plain text", func() {
		service := newService(GinkgoT(), newFixture(GinkgoT()))
		delta, err := service.Render(context.Background(), vaultName, "Delta.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(delta).To(ContainSubstring("Nope"))
		Expect(delta).NotTo(ContainSubstring(`href="/v/private-personal/page/Nope.md"`))
	})

	// The traversal case stays here even though the symlink guard moved to the
	// vault service: path validation still happens before any read.
	DescribeTable("returns a NotFoundError",
		func(vault, pagePath string) {
			service := newService(GinkgoT(), newFixture(GinkgoT()))
			_, err := service.Render(context.Background(), vault, pagePath)
			Expect(err).To(HaveOccurred())
			var notFound pageview.NotFoundError
			Expect(stderrors.As(err, &notFound)).To(BeTrue())
		},
		Entry("unknown vault", "nonexistent", "Alpha.md"),
		Entry("empty path", vaultName, ""),
		Entry("non markdown", vaultName, "Alpha"),
		Entry("traversal", vaultName, "../../../../etc/passwd"),
		Entry("folder path", vaultName, "24 Goals/"),
	)

	It("reports whether a page exists", func() {
		service := newService(GinkgoT(), newFixture(GinkgoT()))
		ctx := context.Background()

		ok, err := service.Exists(ctx, vaultName, "Alpha.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())

		// A missing page is (false, nil): the vault service's not-found is
		// mapped rather than propagated.
		ok, err = service.Exists(ctx, vaultName, "Missing.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	It("maps a vault-service not-found to a page NotFoundError", func() {
		source := errorSource{err: vaultapi.NotFoundError{Message: "not found"}}
		service := pageview.NewService(fixtureVaults{dir: GinkgoT().TempDir()}, source)
		_, err := service.Render(context.Background(), vaultName, "Alpha.md")
		Expect(err).To(HaveOccurred())
		var notFound pageview.NotFoundError
		Expect(stderrors.As(err, &notFound)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("Page not found: "))
	})

	It("wraps a generic read error and does not classify it as not-found", func() {
		source := errorSource{err: stderrors.New("permission denied")}
		service := pageview.NewService(fixtureVaults{dir: GinkgoT().TempDir()}, source)
		_, err := service.Render(context.Background(), vaultName, "Alpha.md")
		Expect(err).To(HaveOccurred())
		var notFound pageview.NotFoundError
		Expect(stderrors.As(err, &notFound)).To(BeFalse())
	})

	It("fails the render when the backlink read fails", func() {
		source := backlinkErrorSource{
			page: &vaultapi.Page{
				Name:    "Alpha",
				Content: "# Alpha\n",
				Links:   map[string]string{},
				Embeds:  map[string]string{},
			},
			err: stderrors.New("vault-api unreachable"),
		}
		service := pageview.NewService(fixtureVaults{dir: GinkgoT().TempDir()}, source)
		_, err := service.Render(context.Background(), vaultName, "Alpha.md")
		Expect(err).To(HaveOccurred())
	})

	It("renders a page without frontmatter", func() {
		service := newService(GinkgoT(), newFolderFixture(GinkgoT()))
		got, err := service.Render(context.Background(), vaultName, "50 Knowledge Base/Plain Page.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring("No frontmatter body marker."))
		Expect(got).NotTo(ContainSubstring("parse frontmatter"))
	})

	It("resolves a wikilink to a page in another folder", func() {
		service := newService(GinkgoT(), newFolderFixture(GinkgoT()))
		got, err := service.Render(context.Background(), vaultName, "24 Goals/Cross Folder Goal.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring(`href="/v/private-personal/page/25%20Tasks/Cross%20Folder%20Task.md"`))
	})

	It("lists a backlink from another folder", func() {
		service := newService(GinkgoT(), newFolderFixture(GinkgoT()))
		got, err := service.Render(context.Background(), vaultName, "25 Tasks/Cross Folder Task.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring(`href="/v/private-personal/page/24%20Goals/Cross%20Folder%20Goal.md"`))
		Expect(got).To(ContainSubstring("Cross Folder Goal"))
	})
})

var _ = Describe("Frontmatter metadata table", func() {
	// A quoted-scalar frontmatter wikilink must render as a real anchor through
	// this package's resolver, not as literal [[...]] text.
	It("renders a quoted frontmatter wikilink as an anchor", func() {
		source := &recordingSource{
			page: &vaultapi.Page{
				Name:    "Goal",
				Content: "---\ngoals: '[[Target Goal]]'\n---\n\nBody.",
				Links:   map[string]string{"Target Goal": "24 Goals/Target Goal.md"},
				Embeds:  map[string]string{},
			},
		}
		service := pageview.NewService(fixtureVaults{}, source)

		got, err := service.Render(context.Background(), vaultName, "Goal.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring(
			`<td>goals</td><td><a class="wikilink" href="/v/private-personal/page/24%20Goals/Target%20Goal.md">Target Goal</a></td>`,
		))
	})

	// An unquoted multi-link frontmatter value previously failed the whole page
	// with a YAML parse error. It must now render both links.
	It("renders an unquoted multi-link frontmatter value", func() {
		source := &recordingSource{
			page: &vaultapi.Page{
				Name:    "Tags",
				Content: "---\ntags: [[Go]] [[Trading]]\n---\n\nBody.",
				Links:   map[string]string{"Go": "Go.md", "Trading": "Trading.md"},
				Embeds:  map[string]string{},
			},
		}
		service := pageview.NewService(fixtureVaults{}, source)

		got, err := service.Render(context.Background(), vaultName, "Tags.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring(
			`<a class="wikilink" href="/v/private-personal/page/Go.md">Go</a>`,
		))
		Expect(got).To(ContainSubstring(
			`<a class="wikilink" href="/v/private-personal/page/Trading.md">Trading</a>`,
		))
	})
})

var _ = Describe("PageURL", func() {
	DescribeTable("escapes each segment and preserves the slashes",
		func(pagePath, want string) {
			Expect(pageview.PageURL(vaultName, pagePath)).To(Equal(want))
		},
		Entry("simple", "Beta.md", "/v/private-personal/page/Beta.md"),
		Entry("nested", "24 Goals/Some Goal.md", "/v/private-personal/page/24%20Goals/Some%20Goal.md"),
	)
})

// The symlink-escape test is deleted with the file read: the vault service now
// owns the guard and refuses to serve a page reached through a symlink. The
// traversal case above still covers the page view's own path validation.
