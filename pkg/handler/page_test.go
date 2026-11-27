// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler_test

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	markdown "github.com/bborbe/vault-markdown"

	"github.com/bborbe/vault-ui/pkg/board"
	"github.com/bborbe/vault-ui/pkg/events"
	"github.com/bborbe/vault-ui/pkg/handler"
	"github.com/bborbe/vault-ui/pkg/pageview"
	"github.com/bborbe/vault-ui/pkg/vaultapi"
)

const pageTestVault = "private-personal"

const alphaSource = `---
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

const betaSource = "---\ntitle: Beta\n---\n\n# Beta\n\nBeta body text.\n"

// pageTestVaults is a board.VaultsProvider over one fixture vault.
type pageTestVaults struct{ dir string }

func (v pageTestVaults) Vaults(context.Context) ([]board.Vault, error) {
	return []board.Vault{{
		Name:      pageTestVault,
		VaultName: pageTestVault,
		Path:      v.dir,
	}}, nil
}

// newPageFixture writes the fixture vault and returns its directory.
func newPageFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"Alpha.md": alphaSource,
		"Beta.md":  betaSource,
		"Gamma.md": "---\ntitle: Gamma\n---\n\n# Gamma\n\n![[Beta]]\n",
		"Delta.md": "---\ntitle: Delta\n---\n\n# Delta\n\n[[Nope]]\n",
		"Links.md": "---\ntitle: Links\n---\n\n# Links\n\n[[Alpha]]\n",
		"Echo.md":  "---\ntitle: Echo\n---\n\n# Echo\n\n<script>alert(1)</script>\n",
		filepath.Join("24 Goals", "Some Goal.md"): "---\ntitle: Some Goal\n---\n\n# Some Goal\n",
	}
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("create fixture dir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	return dir
}

// pageTestSource is a PageSource over the fixture vault. It stands in for the
// vault service: a page read answers the body plus the link and embed maps the
// renderer needs, and backlinks are not exercised by these route specs.
type pageTestSource struct{ dir string }

func (s pageTestSource) Page(
	_ context.Context,
	_ string,
	pagePath string,
) (*vaultapi.Page, error) {
	raw, err := os.ReadFile(filepath.Join(s.dir, filepath.FromSlash(pagePath)))
	if err != nil {
		return nil, vaultapi.NotFoundError{Message: "page not found: " + pagePath}
	}
	links := map[string]string{}
	embeds := map[string]string{}
	_ = filepath.WalkDir(s.dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(s.dir, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		name := strings.TrimSuffix(d.Name(), ".md")
		links[name] = rel
		if body, readErr := os.ReadFile(p); readErr == nil {
			embeds[name] = string(body)
		}
		return nil
	})
	return &vaultapi.Page{
		Vault:   pageTestVault,
		Path:    pagePath,
		Name:    strings.TrimSuffix(path.Base(pagePath), ".md"),
		Content: string(raw),
		Links:   links,
		Embeds:  embeds,
	}, nil
}

func (s pageTestSource) Backlinks(
	context.Context, string, string,
) ([]vaultapi.Backlink, error) {
	return nil, nil
}

// newPageTestRouter builds the real router with a real page service over the
// fixture vault.
func newPageTestRouter(t *testing.T, vaultDir string) http.Handler {
	t.Helper()
	pages := pageview.NewService(pageTestVaults{dir: vaultDir}, pageTestSource{dir: vaultDir})
	staticFS := fstest.MapFS{
		"index.html": {Data: []byte("<html>index</html>")},
	}
	return handler.CreateHTTPRouter(
		&fakeBoard{}, &fakeMutations{}, staticFS, events.NewHub(), pages,
	)
}

func doPageRequest(
	router http.Handler, method, target string,
) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	return recorder
}

func TestPageRouteRendersReleasedRendererOutput(t *testing.T) {
	vaultDir := newPageFixture(t)
	router := newPageTestRouter(t, vaultDir)

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
	).Render(context.Background(), alphaSource)
	if err != nil {
		t.Fatalf("render expected output: %v", err)
	}

	recorder := doPageRequest(
		router, http.MethodGet, "/v/private-personal/page/Alpha.md",
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, expected) {
		t.Fatalf("body does not contain the renderer output:\n%s", body)
	}
	if !strings.Contains(body, "<table>") {
		t.Fatalf("body does not contain a rendered table:\n%s", body)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", contentType)
	}
}

func TestPageRouteRendersAndIsReadOnly(t *testing.T) {
	vaultDir := newPageFixture(t)
	router := newPageTestRouter(t, vaultDir)

	alpha := doPageRequest(router, http.MethodGet, "/v/private-personal/page/Alpha.md")
	if alpha.Code != http.StatusOK {
		t.Fatalf("alpha status = %d, want 200", alpha.Code)
	}
	if !strings.Contains(alpha.Body.String(), "<table>") {
		t.Fatalf("alpha body has no table")
	}
	if !strings.Contains(alpha.Body.String(), "Important Title") {
		t.Fatalf("alpha body has no callout title")
	}

	beta := doPageRequest(router, http.MethodGet, "/v/private-personal/page/Beta.md")
	if beta.Code != http.StatusOK {
		t.Fatalf("beta status = %d, want 200", beta.Code)
	}
	if alpha.Body.String() == beta.Body.String() {
		t.Fatalf("alpha and beta bodies are identical")
	}

	for _, method := range []string{
		http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
	} {
		recorder := doPageRequest(router, method, "/v/private-personal/page/Alpha.md")
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want 405", method, recorder.Code)
		}
	}
}

func TestPageRouteRendersScriptAsText(t *testing.T) {
	vaultDir := newPageFixture(t)
	router := newPageTestRouter(t, vaultDir)

	recorder := doPageRequest(router, http.MethodGet, "/v/private-personal/page/Echo.md")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("page body carries an executable script tag:\n%s", body)
	}
	if strings.Contains(body, "alert(1)") {
		t.Fatalf("page body carries the script text:\n%s", body)
	}
	if !strings.Contains(body, "<!-- raw HTML omitted -->") {
		t.Fatalf("page body does not record that the raw HTML was dropped:\n%s", body)
	}
}

func TestPageRouteFallsBackToStaticTree(t *testing.T) {
	vaultDir := newPageFixture(t)
	router := newPageTestRouter(t, vaultDir)

	recorder := doPageRequest(router, http.MethodGet, "/")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "index") {
		t.Fatalf("static fallback body = %q", recorder.Body.String())
	}
}

func TestOpenRedirectsObsidianParameters(t *testing.T) {
	vaultDir := newPageFixture(t)
	router := newPageTestRouter(t, vaultDir)

	recorder := doPageRequest(
		router, http.MethodGet, "/open?vault=private-personal&file=Alpha.md",
	)
	if recorder.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", recorder.Code)
	}
	if location := recorder.Header().Get("Location"); location != "/v/private-personal/page/Alpha.md" {
		t.Fatalf("Location = %q", location)
	}
}

func TestOpenMissingPageIsNotFound(t *testing.T) {
	vaultDir := newPageFixture(t)
	router := newPageTestRouter(t, vaultDir)

	recorder := doPageRequest(
		router, http.MethodGet, "/open?vault=private-personal&file=Missing.md",
	)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing page status = %d, want 404", recorder.Code)
	}
}

func TestOpenUnknownVaultIsNotFound(t *testing.T) {
	vaultDir := newPageFixture(t)
	router := newPageTestRouter(t, vaultDir)

	recorder := doPageRequest(
		router, http.MethodGet, "/open?vault=nonexistent&file=Alpha.md",
	)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown vault status = %d, want 404", recorder.Code)
	}
}

func TestOpenRefusesNonGet(t *testing.T) {
	vaultDir := newPageFixture(t)
	router := newPageTestRouter(t, vaultDir)

	recorder := doPageRequest(
		router, http.MethodPost, "/open?vault=private-personal&file=Alpha.md",
	)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", recorder.Code)
	}
}

func TestOpenRedirectsEncodedNestedPath(t *testing.T) {
	vaultDir := newPageFixture(t)
	router := newPageTestRouter(t, vaultDir)

	recorder := doPageRequest(
		router, http.MethodGet,
		"/open?vault=private-personal&file=24%20Goals%2FSome%20Goal.md",
	)
	if recorder.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", recorder.Code)
	}
	want := "/v/private-personal/page/24%20Goals/Some%20Goal.md"
	if location := recorder.Header().Get("Location"); location != want {
		t.Fatalf("Location = %q, want %q", location, want)
	}
}
