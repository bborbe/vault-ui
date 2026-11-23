// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pageview renders a vault page to HTML in-process, reusing the
// released github.com/bborbe/vault-markdown renderer and a vault-wide page read.
// The wikilink/backlink index spans the whole vault, not one folder, so a goal
// linking a task resolves. The index is cached briefly to keep a page view cheap.
package pageview

import (
	"context"
	"html/template"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	stderrors "errors"

	"github.com/bborbe/errors"
	"github.com/bborbe/vault-cli/pkg/domain"
	markdown "github.com/bborbe/vault-markdown"
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/board"
)

// indexTTL bounds how stale the cached vault index may be. A page view is a
// read-only surface; a 30s lag after an edit is acceptable and keeps the walk
// off the hot path.
const indexTTL = 30 * time.Second

// PageStorage is the narrow page-read seam the service reads through. It is
// satisfied by vault-cli's storage.PageStorage.
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

// vaultIndex is the vault-wide page map: name→body and name→relative path for
// the resolver, and relative path→body for backlinks. It is immutable once
// published, so concurrent renders may read it without a lock; a page that must
// differ from the cached copy is passed to the renderer, never written here.
type vaultIndex struct {
	built  time.Time
	byName map[string]string
	paths  map[string]string
	bodies map[string]string
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
	return &service{vaults: vaults, pages: pages, index: map[string]*vaultIndex{}}
}

type service struct {
	vaults board.VaultsProvider
	pages  PageStorage

	mu    sync.Mutex
	index map[string]*vaultIndex
}

// Render reads the requested page, builds (or reuses) the vault-wide index, and
// renders the page wrapped in a document.
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
	idx, err := s.loadIndex(ctx, vault)
	if err != nil {
		return "", err
	}
	body := string(page.Content)
	rendered, err := newRenderer(vault.Name, idx, pagePath, name, body).Render(ctx, body)
	if err != nil {
		return "", errors.Wrap(ctx, err, "render page")
	}
	backlinks := markdown.Backlinks(idx.bodies, name)
	return buildDocument(ctx, vault, pagePath, name, rendered, backlinks)
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
// NotFoundError. A page without frontmatter is read raw and rendered rather than
// rejected — the vault holds real pages that carry none.
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
		if isNoFrontmatter(err) {
			full := filepath.Join(vault.Path, pagesDir, name+".md")
			raw, readErr := readVaultFile(ctx, full)
			if readErr != nil {
				if isMissing(readErr) {
					return nil, NotFoundError{Message: "Page not found: " + path.Join(pagesDir, name)}
				}
				return nil, readErr
			}
			return &domain.Page{
				FileMetadata: domain.FileMetadata{Name: name, FilePath: full},
				Content:      domain.Content(raw),
			}, nil
		}
		return nil, err
	}
	return page, nil
}

// readVaultFile reads a page file, refusing a symlink so a page cannot escape
// the vault through one. Mirrors the storage layer's symlink guard.
func readVaultFile(ctx context.Context, full string) (string, error) {
	info, err := os.Lstat(full)
	if err != nil {
		return "", errors.Wrap(ctx, err, "lstat page")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.Errorf(ctx, "symlink outside vault: %s", full)
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return "", errors.Wrap(ctx, err, "read page")
	}
	return string(raw), nil
}

// loadIndex returns the cached vault-wide index, rebuilding it when older than
// indexTTL. It walks the whole vault so cross-folder links resolve.
func (s *service) loadIndex(ctx context.Context, vault board.Vault) (*vaultIndex, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx, ok := s.index[vault.Path]; ok && time.Since(idx.built) < indexTTL {
		return idx, nil
	}
	idx := &vaultIndex{
		byName: map[string]string{},
		paths:  map[string]string{},
		bodies: map[string]string{},
	}
	root := vault.Path
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		// Skip a symlinked file: it may point outside the vault, and the walk
		// must not follow it.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		raw, readErr := os.ReadFile(p)
		if readErr != nil {
			glog.V(2).Infof("skip page %s for page view: %v", rel, readErr)
			return nil
		}
		name := strings.TrimSuffix(d.Name(), ".md")
		body := string(raw)
		idx.byName[name] = body
		idx.paths[name] = rel
		idx.bodies[rel] = body
		return nil
	})
	if err != nil {
		return nil, errors.Wrap(ctx, err, "walk vault")
	}
	idx.built = time.Now()
	s.index[vault.Path] = idx
	return idx, nil
}

// newRenderer builds the vault-markdown renderer with a resolver over the
// vault-wide name→path index, so a link to a page in any folder resolves. The
// requested page is preferred over the cached index for its own link target and
// its embed source, so a just-edited page renders from its fresh body without
// mutating the shared index that concurrent renders read.
func newRenderer(
	vaultName string,
	idx *vaultIndex,
	pagePath string,
	pageName string,
	pageBody string,
) markdown.Renderer {
	return markdown.New(
		markdown.WithResolver(markdown.ResolverFunc(func(target string) (string, bool) {
			if target == pageName {
				return PageURL(vaultName, pagePath), true
			}
			rel, ok := idx.paths[target]
			if !ok {
				return "", false
			}
			return PageURL(vaultName, rel), true
		})),
		markdown.WithPages(markdown.PagesFunc(func(name string) (string, bool) {
			if name == pageName {
				return pageBody, true
			}
			body, ok := idx.byName[name]
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

// isNoFrontmatter reports whether the storage reader rejected the page only
// because it carries no frontmatter block.
func isNoFrontmatter(err error) bool {
	return strings.Contains(err.Error(), "no frontmatter found")
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
// inserted as HTML; every other value is escaped by html/template. backlinks are
// vault-relative paths.
func buildDocument(
	ctx context.Context,
	vault board.Vault,
	pagePath string,
	name string,
	rendered string,
	backlinks []string,
) (string, error) {
	entries := make([]backlink, 0, len(backlinks))
	for _, rel := range backlinks {
		linkName := strings.TrimSuffix(path.Base(rel), ".md")
		if linkName == name {
			continue
		}
		entries = append(entries, backlink{
			Name: linkName,
			URL:  PageURL(vault.Name, rel),
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
