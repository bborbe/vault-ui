// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/bborbe/vault-cli/pkg/storage"
	markdown "github.com/bborbe/vault-markdown"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/board"
	"github.com/bborbe/vault-ui/pkg/handler"
	"github.com/bborbe/vault-ui/pkg/pageview"
	"github.com/bborbe/vault-ui/pkg/websocket"
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

// newPageTestRouter builds the real router with a real page service over the
// fixture vault.
func newPageTestRouter(t *testing.T, vaultDir string) http.Handler {
	t.Helper()
	pages := pageview.NewService(pageTestVaults{dir: vaultDir}, storage.NewPageStorage(nil))
	staticFS := fstest.MapFS{
		"index.html": {Data: []byte("<html>index</html>")},
	}
	readiness := vaultui.NewReadiness()
	readiness.SetReady()
	manager := websocket.NewConnectionManager(websocket.NewMetrics())
	return handler.CreateHTTPRouter(
		&fakeBoard{}, &fakeMutations{}, staticFS, readiness, manager, pages,
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
