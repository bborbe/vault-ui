// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pageview renders a vault page to HTML in-process, reusing the
// released github.com/bborbe/vault-markdown renderer and the board's on-demand
// page read. Every render reads the page from disk; nothing is cached.
package pageview

import (
	"context"
	"html/template"
	"io/fs"
	"net/url"
	"path"
	"strings"

	stderrors "errors"

	"github.com/bborbe/errors"
	"github.com/bborbe/vault-cli/pkg/domain"
	markdown "github.com/bborbe/vault-markdown"
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/board"
)

// PageStorage is the narrow page-index seam the service reads through. It is
// satisfied by pageindex.PageIndex and by vault-cli's storage.PageStorage.
type PageStorage interface {
	ListPages(ctx context.Context, vaultPath string, pagesDir string) ([]*domain.Page, error)
	ReadPage(
		ctx context.Context,
		vaultPath string,
		pagesDir string,
		name string,
	) (*domain.Page, error)
}

// NotFoundError reports a vault, path or page the route cannot serve as a 404.
// It is returned as a value so callers can classify it with stderrors.As.
type NotFoundError struct{ Message string }

// Error implements error.
func (e NotFoundError) Error() string { return e.Message }

// Service renders a page and reports whether one exists.
type Service interface {
	// Render returns the rendered HTML document for a vault-relative page path.
	Render(ctx context.Context, vaultName string, pagePath string) (string, error)
	// Exists reports whether the page can be served.
	Exists(ctx context.Context, vaultName string, pagePath string) (bool, error)
}

// PageURL returns /v/{vault}/page/{path} with each path segment escaped and the
// slashes between segments preserved.
func PageURL(vaultName, pagePath string) string {
	segments := strings.Split(pagePath, "/")
	escaped := make([]string, len(segments))
	for i, segment := range segments {
		escaped[i] = url.PathEscape(segment)
	}
	return "/v/" + url.PathEscape(vaultName) + "/page/" + strings.Join(escaped, "/")
}

// NewService returns the page-rendering service over the vault set and the
// page-index read seam.
func NewService(vaults board.VaultsProvider, pages PageStorage) Service {
	return &service{vaults: vaults, pages: pages}
}

type service struct {
	vaults board.VaultsProvider
	pages  PageStorage
}

// Render reads the requested page, lists its folder once to build the
// wikilink/embed/backlink maps, and renders the page wrapped in a document.
func (s *service) Render(
	ctx context.Context,
	vaultName string,
	pagePath string,
) (string, error) {
	vault, err := s.resolveVault(ctx, vaultName)
	if err != nil {
		return "", err
	}
	pagesDir, name, err := splitPagePath(pagePath)
	if err != nil {
		return "", err
	}
	page, err := s.readPage(ctx, vault, pagesDir, name)
	if err != nil {
		return "", err
	}
	pagesByName, backlinkPages, err := s.loadPages(ctx, vault, pagesDir, page)
	if err != nil {
		return "", err
	}
	rendered, err := newRenderer(vault.Name, pagesDir, pagesByName).Render(
		ctx,
		string(page.Content),
	)
	if err != nil {
		return "", errors.Wrap(ctx, err, "render page")
	}
	backlinks := markdown.Backlinks(backlinkPages, name)
	return buildDocument(ctx, vault, pagePath, name, rendered, backlinks, pagesDir)
}

// Exists resolves the vault and path, then reads the page once. A missing page,
// an unknown vault or an invalid path is a NotFoundError.
func (s *service) Exists(
	ctx context.Context,
	vaultName string,
	pagePath string,
) (bool, error) {
	vault, err := s.resolveVault(ctx, vaultName)
	if err != nil {
		return false, err
	}
	pagesDir, name, err := splitPagePath(pagePath)
	if err != nil {
		return false, err
	}
	if _, err := s.readPage(ctx, vault, pagesDir, name); err != nil {
		return false, err
	}
	return true, nil
}

// resolveVault finds a configured vault by name. An unknown name is a 404.
func (s *service) resolveVault(
	ctx context.Context,
	vaultName string,
) (board.Vault, error) {
	vaults, err := s.vaults.Vaults(ctx)
	if err != nil {
		return board.Vault{}, errors.Wrap(ctx, err, "load vaults")
	}
	for _, vault := range vaults {
		// The board's Obsidian deep links carry the vault's display name
		// (VaultName), while the config key is Name. The page route must accept
		// both, case-insensitively, or a card's in-app link and a real
		// obsidian:// link both 404 for every vault whose display name differs
		// from its key.
		if strings.EqualFold(vault.Name, vaultName) || strings.EqualFold(vault.VaultName, vaultName) {
			return vault, nil
		}
	}
	return board.Vault{}, NotFoundError{Message: "Unknown vault: " + vaultName}
}

// readPage reads one page and maps a missing file or a symlink escape to a
// NotFoundError. Any other read error is returned unchanged for a 500.
func (s *service) readPage(
	ctx context.Context,
	vault board.Vault,
	pagesDir string,
	name string,
) (*domain.Page, error) {
	page, err := s.pages.ReadPage(ctx, vault.Path, pagesDir, name)
	if err != nil {
		if isMissing(err) {
			return nil, NotFoundError{Message: "Page not found: " + path.Join(pagesDir, name)}
		}
		return nil, err
	}
	return page, nil
}

// loadPages lists the folder exactly once and reads every other page once to
// build the name-keyed body map and the filename-keyed backlink map. The
// requested page's body is reused, never read a second time.
func (s *service) loadPages(
	ctx context.Context,
	vault board.Vault,
	pagesDir string,
	requested *domain.Page,
) (map[string]string, map[string]string, error) {
	listed, err := s.pages.ListPages(ctx, vault.Path, pagesDir)
	if err != nil {
		return nil, nil, errors.Wrap(ctx, err, "list pages")
	}
	body := string(requested.Content)
	pagesByName := make(map[string]string, len(listed)+1)
	backlinkPages := make(map[string]string, len(listed)+1)
	pagesByName[requested.Name] = body
	backlinkPages[requested.Name+".md"] = body
	for _, page := range listed {
		if page == nil || page.Name == requested.Name {
			continue
		}
		read, readErr := s.pages.ReadPage(ctx, vault.Path, pagesDir, page.Name)
		if readErr != nil {
			glog.V(2).Infof("skip page %s for page view: %v", page.Name, readErr)
			continue
		}
		pagesByName[page.Name] = string(read.Content)
		backlinkPages[page.Name+".md"] = string(read.Content)
	}
	return pagesByName, backlinkPages, nil
}

// newRenderer builds the vault-markdown renderer with the resolver and pages
// adapters over the folder's name-keyed body map.
func newRenderer(vaultName, pagesDir string, pagesByName map[string]string) markdown.Renderer {
	return markdown.New(
		markdown.WithResolver(markdown.ResolverFunc(func(target string) (string, bool) {
			if _, ok := pagesByName[target]; !ok {
				return "", false
			}
			return PageURL(vaultName, path.Join(pagesDir, target+".md")), true
		})),
		markdown.WithPages(markdown.PagesFunc(func(name string) (string, bool) {
			body, ok := pagesByName[name]
			return body, ok
		})),
	)
}

// splitPagePath validates the vault-relative path before any storage call and
// splits it into the pages directory and the bare page name.
func splitPagePath(pagePath string) (string, string, error) {
	if pagePath == "" || strings.HasPrefix(pagePath, "/") || !strings.HasSuffix(pagePath, ".md") {
		return "", "", NotFoundError{Message: "Page not found: " + pagePath}
	}
	segments := strings.Split(pagePath, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", "", NotFoundError{Message: "Page not found: " + pagePath}
		}
	}
	name := strings.TrimSuffix(segments[len(segments)-1], ".md")
	if name == "" {
		return "", "", NotFoundError{Message: "Page not found: " + pagePath}
	}
	return strings.Join(segments[:len(segments)-1], "/"), name, nil
}

// isMissing reports whether err means the page cannot be served: a missing
// file, a symlink escaping the vault, or a rejected page name.
func isMissing(err error) bool {
	if stderrors.Is(err, fs.ErrNotExist) {
		return true
	}
	message := err.Error()
	return strings.Contains(message, "symlink outside vault") ||
		strings.Contains(message, "invalid page name")
}

// backlink is one rendered backlinks entry.
type backlink struct {
	Name string
	URL  string
}

// document is the template data for the page wrapper.
type document struct {
	Title string
	// ObsidianURL is a template.URL because html/template refuses an
	// `obsidian://` href as an unsafe scheme and renders `#ZgotmplZ` instead.
	ObsidianURL template.URL
	Body        template.HTML
	Backlinks   []backlink
}

// pageTemplate wraps the rendered body in a small document that loads the
// board's existing stylesheet.
var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{.Title}}</title>
<link rel="stylesheet" href="/style.css">
</head>
<body>
<main class="page-view">
<nav class="page-toolbar"><a class="obsidian-link" href="{{.ObsidianURL}}">Open in Obsidian</a></nav>
{{.Body}}
<section class="backlinks">
<h2>Backlinks</h2>
<ul>
{{range .Backlinks}}<li><a href="{{.URL}}">{{.Name}}</a></li>
{{end}}</ul>
</section>
</main>
</body>
</html>
`))

// buildDocument renders the document wrapper. Only the renderer output is
// inserted as HTML; every other value is escaped by html/template.
func buildDocument(
	ctx context.Context,
	vault board.Vault,
	pagePath string,
	name string,
	rendered string,
	backlinks []string,
	pagesDir string,
) (string, error) {
	entries := make([]backlink, 0, len(backlinks))
	for _, filename := range backlinks {
		entries = append(entries, backlink{
			Name: strings.TrimSuffix(filename, ".md"),
			URL:  PageURL(vault.Name, path.Join(pagesDir, filename)),
		})
	}
	var builder strings.Builder
	err := pageTemplate.Execute(&builder, document{
		Title:       name,
		ObsidianURL: template.URL(board.ObsidianURL(vault.VaultName, pagePath)),
		Body:        template.HTML(rendered), //#nosec G203 -- renderer output is trusted HTML
		Backlinks:   entries,
	})
	if err != nil {
		return "", errors.Wrap(ctx, err, "render page document")
	}
	return builder.String(), nil
}
