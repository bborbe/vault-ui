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
	"testing"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/storage"

	"github.com/bborbe/vault-ui/pkg/board"
	"github.com/bborbe/vault-ui/pkg/pageindex"
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
		"Beta.md":  "---\ntitle: Beta\n---\n\n# Beta\n\nBeta body text.\n",
		"Gamma.md": "---\ntitle: Gamma\n---\n\n# Gamma\n\n![[Beta]]\n",
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

// newService builds the real service over the production page-index seam.
func newService(t *testing.T, dir string) pageview.Service {
	t.Helper()
	index := pageindex.NewPageIndex(
		pageindex.NewPageReader(storage.NewPageStorage(nil)),
		pageindex.NewDirectoryLister(),
		libtime.NewCurrentDateTime(),
		libtime.NewWaiterDuration(),
	)
	return pageview.NewService(fixtureVaults{dir: dir}, index)
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
	index := pageindex.NewPageIndex(
		pageindex.NewPageReader(storage.NewPageStorage(nil)),
		pageindex.NewDirectoryLister(),
		libtime.NewCurrentDateTime(),
		libtime.NewWaiterDuration(),
	)
	service := pageview.NewService(displayNameVaults{dir: dir}, index)
	for _, name := range []string{"fixture", "Fixture", "FIXTURE"} {
		if _, err := service.Render(context.Background(), name, "Alpha.md"); err != nil {
			t.Fatalf("render with vault name %q: %v", name, err)
		}
	}
}

func TestPageViewRendersWikilinksAndBacklinks(t *testing.T) {
	dir := newFixture(t)
	service := newService(t, dir)
	ctx := context.Background()

	alpha, err := service.Render(ctx, vaultName, "Alpha.md")
	if err != nil {
		t.Fatalf("render alpha: %v", err)
	}
	if !strings.Contains(alpha, `<a class="wikilink" href="/v/private-personal/page/Beta.md">`) {
		t.Fatalf("alpha has no resolved wikilink:\n%s", alpha)
	}
	if !strings.Contains(alpha, `href="/v/private-personal/page/Links.md"`) {
		t.Fatalf("alpha has no backlink to Links:\n%s", alpha)
	}
	if !strings.Contains(alpha, "<table>") {
		t.Fatalf("alpha has no table")
	}

	beta, err := service.Render(ctx, vaultName, "Beta.md")
	if err != nil {
		t.Fatalf("render beta: %v", err)
	}
	if !strings.Contains(beta, `href="/v/private-personal/page/Alpha.md"`) {
		t.Fatalf("beta has no backlink to Alpha:\n%s", beta)
	}
	if !strings.Contains(beta, `href="/v/private-personal/page/Gamma.md"`) {
		t.Fatalf("beta has no backlink to Gamma:\n%s", beta)
	}
	if strings.Contains(beta, `href="/v/private-personal/page/Delta.md"`) {
		t.Fatalf("beta lists Delta, which does not link to it")
	}
}

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
}

func TestPageViewMissingLinkStaysPlainText(t *testing.T) {
	dir := newFixture(t)
	service := newService(t, dir)

	delta, err := service.Render(context.Background(), vaultName, "Delta.md")
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

func TestPageViewExists(t *testing.T) {
	dir := newFixture(t)
	service := newService(t, dir)
	ctx := context.Background()

	ok, err := service.Exists(ctx, vaultName, "Alpha.md")
	if err != nil {
		t.Fatalf("exists alpha: %v", err)
	}
	if !ok {
		t.Fatalf("alpha should exist")
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
