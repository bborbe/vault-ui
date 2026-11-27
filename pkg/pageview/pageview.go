// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pageview renders a vault page to HTML in-process, reusing the
// released github.com/bborbe/vault-markdown renderer. The page body, its
// wikilink targets and its embeds come from the vault service, and so do its
// backlinks: a render costs two small reads and never scans the vault.
package pageview

import (
	"context"
	"html/template"
	"net/url"
	"path"
	"strings"

	stderrors "errors"

	"github.com/bborbe/errors"
	markdown "github.com/bborbe/vault-markdown"

	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/board"
	"github.com/bborbe/vault-ui/pkg/openable"
	"github.com/bborbe/vault-ui/pkg/vaultapi"
)

// PageSource is the vault-service read seam. It is satisfied by
// vaultapi.Client, so the service holds no index and reads no file.
type PageSource interface {
	Page(ctx context.Context, vaultName string, pagePath string) (*vaultapi.Page, error)
	Backlinks(ctx context.Context, vaultName string, pagePath string) ([]vaultapi.Backlink, error)
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
// vault-service page read seam.
func NewService(vaults board.VaultsProvider, pages PageSource) Service {
	return NewServiceWithHolds(vaults, pages, nil)
}

// NewServiceWithHolds is NewService plus claude-wrapper's hold reasons: a task,
// goal or topic page the wrapper holds renders the same reason badge its board
// card shows. A nil holds renders no badge.
func NewServiceWithHolds(
	vaults board.VaultsProvider,
	pages PageSource,
	holds openable.Lookup,
) Service {
	return &service{vaults: vaults, pages: pages, holds: holds}
}

type service struct {
	vaults board.VaultsProvider
	pages  PageSource
	holds  openable.Lookup
}

// Render reads the requested page and its backlinks from the vault service and
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
	dir, name, err := splitPagePath(pagePath)
	if err != nil {
		return "", err
	}
	page, err := s.readPage(ctx, vault, pagePath)
	if err != nil {
		return "", err
	}
	rendered, err := newRenderer(vault.Name, pagePath, name, page).Render(ctx, page.Content)
	if err != nil {
		return "", errors.Wrap(ctx, err, "render page")
	}
	links, err := s.pages.Backlinks(ctx, vault.Name, pagePath)
	if err != nil {
		return "", errors.Wrap(ctx, err, "read backlinks")
	}
	backlinks := make([]string, 0, len(links))
	for _, link := range links {
		backlinks = append(backlinks, link.Path)
	}
	return buildDocument(ctx, vault, pagePath, name, rendered, backlinks, s.holdFor(vault, dir, name))
}

// holdFor resolves the page's kind from the folder it lives in and returns the
// wrapper's hold for it, or nil for a page outside the task, goal and topic
// folders or one the wrapper does not hold.
func (s *service) holdFor(vault board.Vault, dir, name string) *api.HoldResponse {
	if s.holds == nil {
		return nil
	}
	var kind string
	switch {
	case dir != "" && dir == vault.TasksFolder:
		kind = "task"
	case dir != "" && dir == vault.GoalsFolder:
		kind = "goal"
	case dir != "" && dir == vault.TopicsFolder:
		kind = "topic"
	default:
		return nil
	}
	if hold := s.holds.Hold(vault.VaultName, kind, name); hold != nil {
		return hold
	}
	if vault.Name != vault.VaultName {
		return s.holds.Hold(vault.Name, kind, name)
	}
	return nil
}

// Exists resolves the vault and path, then asks the vault service for the page
// once. A missing page is (false, nil); any other failure is returned.
func (s *service) Exists(
	ctx context.Context,
	vaultName string,
	pagePath string,
) (bool, error) {
	vault, err := s.resolveVault(ctx, vaultName)
	if err != nil {
		return false, err
	}
	if _, _, err := splitPagePath(pagePath); err != nil {
		return false, err
	}
	if _, err := s.readPage(ctx, vault, pagePath); err != nil {
		var notFound NotFoundError
		if stderrors.As(err, &notFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// readPage asks the vault service for one page. A not-found is mapped to the
// package's NotFoundError; any other error is returned wrapped and is not a
// NotFoundError.
func (s *service) readPage(
	ctx context.Context,
	vault board.Vault,
	pagePath string,
) (*vaultapi.Page, error) {
	page, err := s.pages.Page(ctx, vault.Name, pagePath)
	if err != nil {
		var notFound vaultapi.NotFoundError
		if stderrors.As(err, &notFound) {
			return nil, NotFoundError{Message: "Page not found: " + pagePath}
		}
		return nil, errors.Wrap(ctx, err, "read page")
	}
	return page, nil
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

// newRenderer builds the vault-markdown renderer over the page's link and embed
// maps, so a link to a page in any folder resolves. The requested page is
// preferred for its own link target and embed source.
func newRenderer(
	vaultName string,
	pagePath string,
	pageName string,
	page *vaultapi.Page,
) markdown.Renderer {
	return markdown.New(
		markdown.WithResolver(markdown.ResolverFunc(func(target string) (string, bool) {
			if target == pageName {
				return PageURL(vaultName, pagePath), true
			}
			rel, ok := page.Links[target]
			if !ok {
				return "", false
			}
			return PageURL(vaultName, rel), true
		})),
		markdown.WithPages(markdown.PagesFunc(func(name string) (string, bool) {
			if name == pageName {
				return page.Content, true
			}
			body, ok := page.Embeds[name]
			return body, ok
		})),
	)
}

// splitPagePath validates the vault-relative path before any read and splits it
// into the pages directory and the bare page name.
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
	// Hold is the wrapper's hold reason badge, nil when there is none.
	Hold *holdBadge
}

// holdBadge is the page header's rendering of a wrapper hold.
type holdBadge struct {
	Reason  string
	Label   string
	Title   string
	Summary string
	// PRURL is a template.URL only after it is checked to be http(s).
	PRURL template.URL
}

// newHoldBadge renders a hold for the page header. The PR link is kept only
// for an http(s) URL so a hostile value cannot become a script href.
func newHoldBadge(hold *api.HoldResponse) *holdBadge {
	if hold == nil {
		return nil
	}
	badge := &holdBadge{Reason: hold.Reason, Label: hold.Label, Title: "held: " + hold.Reason}
	if hold.Summary != nil {
		badge.Summary = *hold.Summary
		badge.Title = *hold.Summary + " (held: " + hold.Reason + ")"
	}
	if hold.PRURL != nil {
		if parsed, err := url.Parse(*hold.PRURL); err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") {
			badge.PRURL = template.URL(parsed.String()) //#nosec G203 -- scheme checked above
		}
	}
	return badge
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
<div class="page-header">
<h1 class="page-title">{{.Title}}</h1>
<a class="obsidian-link" href="{{.ObsidianURL}}" title="Open in Obsidian" aria-label="Open in Obsidian">&#8599;</a>
</div>
{{with .Hold}}<div class="page-hold hold-reason" data-reason="{{.Reason}}" title="{{.Title}}"><span class="hold-reason-label">{{.Label}}</span>{{if .Summary}} <span class="hold-reason-summary">{{.Summary}}</span>{{end}}{{if .PRURL}} <a class="hold-reason-pr" href="{{.PRURL}}" target="_blank" rel="noopener">PR</a>{{end}}</div>
{{end}}{{.Body}}
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
	hold *api.HoldResponse,
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
		Hold:        newHoldBadge(hold),
	})
	if err != nil {
		return "", errors.Wrap(ctx, err, "render page document")
	}
	return builder.String(), nil
}
