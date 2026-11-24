// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageview_test

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/storage"
	markdown "github.com/bborbe/vault-markdown"

	"github.com/bborbe/vault-ui/pkg/board"
	"github.com/bborbe/vault-ui/pkg/pageview"
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

const fixtureBeta = "---\ntitle: Beta\n---\n\n# Beta\n\nBeta body text.\n"

const fixtureGamma = "---\ntitle: Gamma\n---\n\n# Gamma\n\n![[Beta]]\n"

type fixtureVaults struct{ dir string }

func (v fixtureVaults) Vaults(context.Context) ([]board.Vault, error) {
	return []board.Vault{{
		Name:      vaultName,
		VaultName: vaultName,
		Path:      v.dir,
	}}, nil
}

// newFixture writes the shared fixture vault and returns its directory.
func newFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"Alpha.md": fixtureAlpha,
		"Beta.md":  fixtureBeta,
		"Gamma.md": fixtureGamma,
		"Delta.md": "---\ntitle: Delta\n---\n\n# Delta\n\n[[Nope]]\n",
		"Links.md": "---\ntitle: Links\n---\n\n# Links\n\n[[Alpha]]\n",
		"Echo.md":  "---\ntitle: Echo\n---\n\n# Echo\n\n<script>alert(1)</script>\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	return dir
}

// newService builds the real service over the production page-read seam.
func newService(t *testing.T, dir string) pageview.Service {
	t.Helper()
	return pageview.NewService(fixtureVaults{dir: dir}, storage.NewPageStorage(nil))
}

// countingStorage wraps a real page storage and records every seam call, keyed
// by folder (and name for reads), so a resolver that re-lists or re-reads fails.
type countingStorage struct {
	inner pageview.PageStorage

	mu        sync.Mutex
	listCalls map[string]int
	readCalls map[string]int
}

func newCountingStorage() *countingStorage {
	return &countingStorage{
		inner:     storage.NewPageStorage(nil),
		listCalls: map[string]int{},
		readCalls: map[string]int{},
	}
}

func (c *countingStorage) ListPages(
	ctx context.Context,
	vaultPath string,
	pagesDir string,
) ([]*domain.Page, error) {
	c.mu.Lock()
	c.listCalls[pagesDir]++
	c.mu.Unlock()
	return c.inner.ListPages(ctx, vaultPath, pagesDir)
}

func (c *countingStorage) ReadPage(
	ctx context.Context,
	vaultPath string,
	pagesDir string,
	name string,
) (*domain.Page, error) {
	c.mu.Lock()
	c.readCalls[pagesDir+"/"+name]++
	c.mu.Unlock()
	return c.inner.ReadPage(ctx, vaultPath, pagesDir, name)
}

func (c *countingStorage) listCount(pagesDir string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.listCalls[pagesDir]
}

func (c *countingStorage) readCount(pagesDir, name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readCalls[pagesDir+"/"+name]
}

// backlinksSection returns the document from the backlinks section onward, so a
// test can tell a backlink apart from a same-named link in the page body.
func backlinksSection(t *testing.T, doc string) string {
	t.Helper()
	const marker = `<section class="backlinks">`
	index := strings.Index(doc, marker)
	if index < 0 {
		t.Fatalf("document has no backlinks section:\n%s", doc)
	}
	return doc[index:]
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

// TestPageViewResolvesVaultByDisplayName guards the card and obsidian:// links:
// both carry the display name, so the resolver must accept it as well as the
// config key, in any case.
func TestPageViewResolvesVaultByDisplayName(t *testing.T) {
	dir := newFixture(t)
	service := pageview.NewService(displayNameVaults{dir: dir}, storage.NewPageStorage(nil))
	for _, name := range []string{"fixture", "Fixture", "FIXTURE"} {
		if _, err := service.Render(context.Background(), name, "Alpha.md"); err != nil {
			t.Fatalf("render with vault name %q: %v", name, err)
		}
	}
}

// TestPageViewUsesReleasedRenderer proves the released renderer produced the
// body: its exact output is contained in the document, and the raw source's
// pipe-table marker is gone.
func TestPageViewUsesReleasedRenderer(t *testing.T) {
	dir := newFixture(t)
	service := newService(t, dir)

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
	if err != nil {
		t.Fatalf("render expected output: %v", err)
	}

	got, err := service.Render(context.Background(), vaultName, "Alpha.md")
	if err != nil {
		t.Fatalf("render alpha: %v", err)
	}
	if !strings.Contains(got, expected) {
		t.Fatalf("document does not contain the released renderer output:\n%s", got)
	}
	if strings.Contains(got, "| --- |") {
		t.Fatalf("document echoes raw markdown instead of rendering it:\n%s", got)
	}
}

// TestPageViewReadsEachPageOnce is the AC3 counting guard: one ListPages for
// the folder and one ReadPage for every page, including the requested one.
func TestPageViewReadsEachPageOnce(t *testing.T) {
	dir := newFixture(t)
	counter := newCountingStorage()
	service := pageview.NewService(fixtureVaults{dir: dir}, counter)

	if _, err := service.Render(context.Background(), vaultName, "Alpha.md"); err != nil {
		t.Fatalf("render alpha: %v", err)
	}

	if got := counter.listCount(""); got != 1 {
		t.Fatalf("ListPages calls for the folder = %d, want 1", got)
	}
	for _, name := range []string{"Alpha", "Beta", "Gamma", "Delta", "Links", "Echo"} {
		if got := counter.readCount("", name); got != 1 {
			t.Fatalf("ReadPage calls for %s = %d, want 1", name, got)
		}
	}
}

// TestPageViewResolvesWikilinks proves a known link resolves to the in-app URL
// and a missing link stays plain text without failing the render.
func TestPageViewResolvesWikilinks(t *testing.T) {
	dir := newFixture(t)
	service := newService(t, dir)
	ctx := context.Background()

	alpha, err := service.Render(ctx, vaultName, "Alpha.md")
	if err != nil {
		t.Fatalf("render alpha: %v", err)
	}
	if !strings.Contains(alpha, `href="/v/private-personal/page/Beta.md"`) {
		t.Fatalf("alpha has no resolved wikilink:\n%s", alpha)
	}

	delta, err := service.Render(ctx, vaultName, "Delta.md")
	if err != nil {
		t.Fatalf("render delta: %v", err)
	}
	if !strings.Contains(delta, "Nope") {
		t.Fatalf("delta lost the missing link text")
	}
	if strings.Contains(delta, `href="/v/private-personal/page/Nope.md"`) {
		t.Fatalf("delta resolved a missing link")
	}
}

// TestPageViewEmbedsPage proves an embed is inlined through WithPages, and that
// the same source rendered without it drops the embed.
func TestPageViewEmbedsPage(t *testing.T) {
	dir := newFixture(t)
	service := newService(t, dir)

	gamma, err := service.Render(context.Background(), vaultName, "Gamma.md")
	if err != nil {
		t.Fatalf("render gamma: %v", err)
	}
	if !strings.Contains(gamma, "Beta body text.") {
		t.Fatalf("gamma does not inline the embedded page:\n%s", gamma)
	}

	withoutPages, err := markdown.New(
		markdown.WithResolver(markdown.ResolverFunc(func(target string) (string, bool) {
			return "/v/private-personal/page/" + target + ".md", true
		})),
	).Render(context.Background(), fixtureGamma)
	if err != nil {
		t.Fatalf("render gamma without pages: %v", err)
	}
	if gamma == withoutPages {
		t.Fatalf("embed output matches the no-pages control; the embed was not inlined")
	}
}

// TestPageViewBacklinks proves the backlinks section lists only the pages that
// link to the requested page, and that a page added after the fixture was built
// appears on the next render (no name-keyed cache).
func TestPageViewBacklinks(t *testing.T) {
	dir := newFixture(t)
	service := newService(t, dir)
	ctx := context.Background()

	alpha, err := service.Render(ctx, vaultName, "Alpha.md")
	if err != nil {
		t.Fatalf("render alpha: %v", err)
	}
	alphaBacklinks := backlinksSection(t, alpha)
	if !strings.Contains(alphaBacklinks, ">Links</a>") {
		t.Fatalf("alpha does not name Links as a backlink:\n%s", alphaBacklinks)
	}
	if strings.Contains(alphaBacklinks, ">Beta</a>") {
		t.Fatalf("alpha names Beta as a backlink, but Beta does not link to it:\n%s", alphaBacklinks)
	}
	if strings.Contains(alphaBacklinks, ">Delta</a>") {
		t.Fatalf("alpha names Delta as a backlink, but Delta does not link to it:\n%s", alphaBacklinks)
	}

	beta, err := service.Render(ctx, vaultName, "Beta.md")
	if err != nil {
		t.Fatalf("render beta: %v", err)
	}
	betaBacklinks := backlinksSection(t, beta)
	if !strings.Contains(betaBacklinks, ">Alpha</a>") {
		t.Fatalf("beta does not name Alpha as a backlink:\n%s", betaBacklinks)
	}
	if !strings.Contains(betaBacklinks, ">Gamma</a>") {
		t.Fatalf("beta does not name Gamma as a backlink:\n%s", betaBacklinks)
	}
	if strings.Contains(betaBacklinks, ">Delta</a>") {
		t.Fatalf("beta names Delta as a backlink, but Delta does not link to it:\n%s", betaBacklinks)
	}

	delta, err := service.Render(ctx, vaultName, "Delta.md")
	if err != nil {
		t.Fatalf("render delta: %v", err)
	}
	if strings.Contains(backlinksSection(t, delta), "Nope") {
		t.Fatalf("delta has a Nope backlink, but no page links to it")
	}

	epsilon := "---\ntitle: Epsilon\n---\n\n# Epsilon\n\n[[Alpha]]\n"
	if err := os.WriteFile(filepath.Join(dir, "Epsilon.md"), []byte(epsilon), 0o600); err != nil {
		t.Fatalf("write Epsilon: %v", err)
	}
	alphaAgain, err := service.Render(ctx, vaultName, "Alpha.md")
	if err != nil {
		t.Fatalf("re-render alpha: %v", err)
	}
	if !strings.Contains(backlinksSection(t, alphaAgain), ">Epsilon</a>") {
		t.Fatalf("alpha does not name a page added after the fixture was built:\n%s", alphaAgain)
	}
}

// TestPageViewDocumentWrapper is the DB8 guard: the document carries the
// stylesheet, and the Obsidian link survives html/template's URL validation
// (a plain string would render as #ZgotmplZ).
func TestPageViewDocumentWrapper(t *testing.T) {
	dir := newFixture(t)
	service := newService(t, dir)

	got, err := service.Render(context.Background(), vaultName, "Alpha.md")
	if err != nil {
		t.Fatalf("render alpha: %v", err)
	}
	for _, want := range []string{
		"<!doctype html>",
		`<meta charset="utf-8">`,
		`<title>Alpha</title>`,
		`<link rel="stylesheet" href="/style.css">`,
		`href="obsidian://open?vault=private-personal&amp;file=Alpha.md"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("document does not contain %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "#ZgotmplZ") {
		t.Fatalf("document escaped the Obsidian URL as unsafe:\n%s", got)
	}
}

func TestPageViewNotFoundCases(t *testing.T) {
	dir := newFixture(t)
	service := newService(t, dir)
	ctx := context.Background()

	cases := []struct {
		name  string
		vault string
		path  string
	}{
		{"unknown vault", "nonexistent", "Alpha.md"},
		{"empty path", vaultName, ""},
		{"non markdown", vaultName, "Alpha"},
		{"traversal", vaultName, "../../../../etc/passwd"},
		{"folder path", vaultName, "24 Goals/"},
		{"backslash name", vaultName, `Foo\Bar.md`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.Render(ctx, tc.vault, tc.path)
			if err == nil {
				t.Fatalf("expected an error")
			}
			var notFound pageview.NotFoundError
			if !stderrors.As(err, &notFound) {
				t.Fatalf("error = %v, want NotFoundError", err)
			}
		})
	}
}

func TestPageViewSymlinkEscapeIsNotFound(t *testing.T) {
	dir := newFixture(t)
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("---\ntitle: Secret\n---\n\n# Secret\n"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "Escape.md")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	service := newService(t, dir)
	_, err := service.Render(context.Background(), vaultName, "Escape.md")
	if err == nil {
		t.Fatalf("expected a NotFoundError for a symlink escaping the vault")
	}
	var notFound pageview.NotFoundError
	if !stderrors.As(err, &notFound) {
		t.Fatalf("error = %v, want NotFoundError", err)
	}
}

func TestPageViewExists(t *testing.T) {
	dir := newFixture(t)
	counter := newCountingStorage()
	service := pageview.NewService(fixtureVaults{dir: dir}, counter)
	ctx := context.Background()

	ok, err := service.Exists(ctx, vaultName, "Alpha.md")
	if err != nil {
		t.Fatalf("exists alpha: %v", err)
	}
	if !ok {
		t.Fatalf("alpha should exist")
	}
	if got := counter.listCount(""); got != 0 {
		t.Fatalf("Exists called ListPages %d times, want 0", got)
	}

	ok, err = service.Exists(ctx, vaultName, "Missing.md")
	if ok {
		t.Fatalf("missing page reported as existing")
	}
	var notFound pageview.NotFoundError
	if !stderrors.As(err, &notFound) {
		t.Fatalf("error = %v, want NotFoundError", err)
	}
}

func TestPageViewPageURL(t *testing.T) {
	cases := map[string]struct{ path, want string }{
		"simple": {"Beta.md", "/v/private-personal/page/Beta.md"},
		"nested": {
			"24 Goals/Some Goal.md",
			"/v/private-personal/page/24%20Goals/Some%20Goal.md",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := pageview.PageURL(vaultName, tc.path); got != tc.want {
				t.Fatalf("PageURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPageViewConcurrentRenderIsRaceFree renders many pages at once so `go test
// -race` catches a reintroduced shared cache. With no shared state it is cheap.
func TestPageViewConcurrentRenderIsRaceFree(t *testing.T) {
	dir := newFixture(t)
	service := newService(t, dir)
	ctx := context.Background()

	const renders = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, renders)
	for i := 0; i < renders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			pagePath := "Alpha.md"
			if i%2 == 1 {
				pagePath = "Beta.md"
			}
			if _, err := service.Render(ctx, vaultName, pagePath); err != nil {
				errs <- err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent render: %v", err)
	}
}

// errorStorage returns a generic read failure.
type errorStorage struct{ err error }

func (e errorStorage) ListPages(context.Context, string, string) ([]*domain.Page, error) {
	return nil, e.err
}

func (e errorStorage) ReadPage(context.Context, string, string, string) (*domain.Page, error) {
	return nil, e.err
}

func TestPageViewGenericReadErrorPropagates(t *testing.T) {
	service := pageview.NewService(fixtureVaults{dir: t.TempDir()}, errorStorage{
		err: stderrors.New("permission denied"),
	})
	_, err := service.Render(context.Background(), vaultName, "Alpha.md")
	if err == nil {
		t.Fatalf("expected an error")
	}
	var notFound pageview.NotFoundError
	if stderrors.As(err, &notFound) {
		t.Fatalf("generic read error classified as NotFoundError: %v", err)
	}
}
