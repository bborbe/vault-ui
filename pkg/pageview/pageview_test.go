// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageview_test

import (
	"context"
	stderrors "errors"
	"html"
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

const fixtureGamma = "---\ntitle: Gamma\n---\n\n# Gamma\n\n![[Beta]]\n"

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
// the whole fixture vault. It reads the vault on every call, so a page written
// after the service was built is visible on the next render — the service adds
// no cache of its own.
type fixtureSource struct{ dir string }

// newFixtureSource returns a source that reads dir on demand.
func newFixtureSource(tb testHelper, dir string) *fixtureSource {
	tb.Helper()
	return &fixtureSource{dir: dir}
}

// snapshot reads every markdown page under the fixture root, keyed by relative
// path and by bare page name.
func (f *fixtureSource) snapshot() (bodies map[string]string, byName map[string]string, err error) {
	bodies = map[string]string{}
	byName = map[string]string{}
	err = filepath.WalkDir(f.dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(f.dir, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		raw, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		bodies[rel] = string(raw)
		byName[strings.TrimSuffix(d.Name(), ".md")] = rel
		return nil
	})
	return bodies, byName, err
}

func (f *fixtureSource) Page(_ context.Context, _ string, pagePath string) (*vaultapi.Page, error) {
	bodies, byName, err := f.snapshot()
	if err != nil {
		return nil, err
	}
	body, ok := bodies[pagePath]
	if !ok {
		return nil, vaultapi.NotFoundError{Message: "page not found: " + pagePath}
	}
	links := make(map[string]string, len(byName))
	embeds := make(map[string]string, len(byName))
	for name, rel := range byName {
		links[name] = rel
		embeds[name] = bodies[rel]
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
	bodies, _, err := f.snapshot()
	if err != nil {
		return nil, err
	}
	if _, ok := bodies[pagePath]; !ok {
		return nil, vaultapi.NotFoundError{Message: "page not found: " + pagePath}
	}
	name := strings.TrimSuffix(path.Base(pagePath), ".md")
	rels := markdown.Backlinks(bodies, name)
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
		"Gamma.md": fixtureGamma,
		"Delta.md": "---\ntitle: Delta\n---\n\n# Delta\n\n[[Nope]]\n",
		"Links.md": "---\ntitle: Links\n---\n\n# Links\n\n[[Alpha]]\n",
		"Echo.md":  "---\ntitle: Echo\n---\n\n# Echo\n\n<script>alert(1)</script>\n",
		filepath.Join("24 Goals", "Some Goal.md"): "---\ntitle: Some Goal\n---\n\n# Some Goal\n\nNested goal body.\n",
	}
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			tb.Fatalf("mkdir fixture %s: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
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

// countingSource records every Page and Backlinks call so a spec can assert
// that an invalid path is refused before any read reaches the seam.
type countingSource struct {
	inner         pageview.PageSource
	pageCalls     int
	backlinkCalls int
}

func (c *countingSource) Page(ctx context.Context, vaultName, pagePath string) (*vaultapi.Page, error) {
	c.pageCalls++
	return c.inner.Page(ctx, vaultName, pagePath)
}

func (c *countingSource) Backlinks(
	ctx context.Context,
	vaultName string,
	pagePath string,
) ([]vaultapi.Backlink, error) {
	c.backlinkCalls++
	return c.inner.Backlinks(ctx, vaultName, pagePath)
}

// backlinksSection returns the document from the backlinks section onward, so a
// spec can assert on backlink entries without matching the rendered body.
func backlinksSection(doc string) string {
	const marker = `<section class="backlinks">`
	idx := strings.Index(doc, marker)
	if idx < 0 {
		return ""
	}
	return doc[idx:]
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

	It("renders a page in a nested folder", func() {
		service := newService(GinkgoT(), newFixture(GinkgoT()))
		got, err := service.Render(context.Background(), vaultName, "24 Goals/Some Goal.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring("Nested goal body."))
		Expect(got).To(ContainSubstring("<title>Some Goal</title>"))
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

var _ = Describe("Page view contract", func() {
	It("embeds the released renderer output verbatim and renders a table", func() {
		service := newService(GinkgoT(), newFixture(GinkgoT()))
		expected, err := markdown.New(
			markdown.WithResolver(markdown.ResolverFunc(func(target string) (string, bool) {
				if target == "Beta" {
					return "/v/private-personal/page/Beta.md", true
				}
				return "", false
			})),
			markdown.WithPages(markdown.PagesFunc(func(string) (string, bool) {
				return "", false
			})),
		).Render(context.Background(), fixtureAlpha)
		Expect(err).NotTo(HaveOccurred())

		got, err := service.Render(context.Background(), vaultName, "Alpha.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring(expected))
		Expect(got).To(ContainSubstring("<table>"))
		Expect(got).NotTo(ContainSubstring("| --- |"))
	})

	It("wraps the body in the board document", func() {
		service := newService(GinkgoT(), newFixture(GinkgoT()))
		got, err := service.Render(context.Background(), vaultName, "Alpha.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring("<!doctype html>"))
		Expect(got).To(ContainSubstring(`<meta charset="utf-8">`))
		Expect(got).To(ContainSubstring(`href="/style.css"`))
		Expect(got).To(ContainSubstring(`<main class="page-view">`))
		Expect(got).To(ContainSubstring(`<section class="backlinks">`))
		Expect(html.UnescapeString(got)).To(ContainSubstring(
			"obsidian://open?vault=private-personal&file=Alpha.md",
		))
	})

	It("drops an embed when the pages source is omitted", func() {
		service := newService(GinkgoT(), newFixture(GinkgoT()))
		got, err := service.Render(context.Background(), vaultName, "Gamma.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring("Beta body text."))

		without, err := markdown.New().Render(context.Background(), fixtureGamma)
		Expect(err).NotTo(HaveOccurred())
		Expect(without).NotTo(ContainSubstring("Beta body text."))
		Expect(got).NotTo(Equal(without))
	})

	It("lists only the pages that link to the requested one", func() {
		service := newService(GinkgoT(), newFixture(GinkgoT()))
		ctx := context.Background()

		alpha, err := service.Render(ctx, vaultName, "Alpha.md")
		Expect(err).NotTo(HaveOccurred())
		alphaBacklinks := backlinksSection(alpha)
		Expect(alphaBacklinks).To(ContainSubstring(">Links</a>"))
		Expect(alphaBacklinks).NotTo(ContainSubstring(">Beta</a>"))
		Expect(alphaBacklinks).NotTo(ContainSubstring(">Gamma</a>"))

		beta, err := service.Render(ctx, vaultName, "Beta.md")
		Expect(err).NotTo(HaveOccurred())
		betaBacklinks := backlinksSection(beta)
		Expect(betaBacklinks).To(ContainSubstring(">Alpha</a>"))
		Expect(betaBacklinks).To(ContainSubstring(">Gamma</a>"))
		Expect(betaBacklinks).NotTo(ContainSubstring(">Delta</a>"))

		delta, err := service.Render(ctx, vaultName, "Delta.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(delta).To(ContainSubstring("Nope"))
		Expect(delta).NotTo(ContainSubstring("Nope.md"))
		Expect(backlinksSection(delta)).NotTo(ContainSubstring("Nope"))
	})

	It("sees a page written after the service was built", func() {
		dir := newFixture(GinkgoT())
		service := newService(GinkgoT(), dir)
		ctx := context.Background()

		before, err := service.Render(ctx, vaultName, "Alpha.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(backlinksSection(before)).NotTo(ContainSubstring("Epsilon"))

		epsilon := "---\ntitle: Epsilon\n---\n\n# Epsilon\n\n[[Alpha]]\n"
		Expect(os.WriteFile(
			filepath.Join(dir, "Epsilon.md"), []byte(epsilon), 0o600,
		)).To(Succeed())

		after, err := service.Render(ctx, vaultName, "Alpha.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(backlinksSection(after)).To(ContainSubstring(">Epsilon</a>"))
	})

	It("refuses an invalid path before any read", func() {
		dir := newFixture(GinkgoT())
		counter := &countingSource{inner: newFixtureSource(GinkgoT(), dir)}
		service := pageview.NewService(fixtureVaults{dir: dir}, counter)

		for _, pagePath := range []string{
			"",
			"Alpha",
			"24 Goals/",
			"../../../../etc/passwd",
			"../../../../etc/passwd.md",
		} {
			_, err := service.Render(context.Background(), vaultName, pagePath)
			Expect(err).To(HaveOccurred())
			var notFound pageview.NotFoundError
			Expect(stderrors.As(err, &notFound)).To(BeTrue(), "path %q", pagePath)
		}
		Expect(counter.pageCalls).To(Equal(0))
		Expect(counter.backlinkCalls).To(Equal(0))
	})

	DescribeTable("reports every not-found as (false, nil) from Exists",
		func(vault, pagePath string) {
			service := newService(GinkgoT(), newFixture(GinkgoT()))
			ok, err := service.Exists(context.Background(), vault, pagePath)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
		},
		Entry("unknown vault", "nonexistent", "Alpha.md"),
		Entry("empty path", vaultName, ""),
		Entry("non markdown", vaultName, "Alpha"),
		Entry("folder path", vaultName, "24 Goals/"),
		Entry("traversal", vaultName, "../../../../etc/passwd"),
		Entry("missing page", vaultName, "Missing.md"),
	)

	It("propagates a generic error from Exists", func() {
		service := pageview.NewService(
			fixtureVaults{dir: GinkgoT().TempDir()},
			errorSource{err: stderrors.New("permission denied")},
		)
		ok, err := service.Exists(context.Background(), vaultName, "Alpha.md")
		Expect(err).To(HaveOccurred())
		Expect(ok).To(BeFalse())
		var notFound pageview.NotFoundError
		Expect(stderrors.As(err, &notFound)).To(BeFalse())
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
