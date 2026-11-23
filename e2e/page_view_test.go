// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build e2e

package e2e

import (
	"strings"

	"github.com/mxschmitt/playwright-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Scenario 040 verifies the in-app page view end to end: a card opens the page
// inside vault-ui, the page renders as HTML, the Obsidian hand-off survives,
// and a wikilink navigates to its resolved target. Each assertion is on the
// navigation response and the rendered body, never on the URL alone — a 404
// served at the same URL would otherwise pass. The vault segment is not
// asserted by casing: the card's link carries the vault's display name and the
// wikilink carries its config name, and both resolve.
var _ = Describe("Scenario 040: the in-app page view", func() {
	It("opens a card's page inside vault-ui and keeps the Obsidian hand-off", func() {
		page := newPage("/?view=tasks")
		defer func() { _ = page.Close() }()

		Eventually(func() int { return countOf(page, ".task-card") }, "15s", "500ms").
			Should(BeNumerically(">", 0), "the board fetches asynchronously")

		card := page.Locator(".task-card", playwright.PageLocatorOptions{HasText: "Card Page Fixture"})
		Eventually(func() int {
			count, err := card.Count()
			Expect(err).NotTo(HaveOccurred())
			return count
		}, "10s", "200ms").Should(Equal(1))

		link := card.Locator(".task-title-link")
		href, err := link.GetAttribute("href")
		Expect(err).NotTo(HaveOccurred())
		Expect(href).To(ContainSubstring("/page/25%20Tasks/Card%20Page%20Fixture.md"))
		Expect(href).NotTo(HavePrefix("obsidian://"))

		resp, err := page.ExpectResponse(func(r playwright.Response) bool {
			return strings.Contains(r.URL(), "Card%20Page%20Fixture")
		}, func() error { return link.Click() })
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Status()).To(Equal(200))
		Expect(page.URL()).To(ContainSubstring("/page/25%20Tasks/Card%20Page%20Fixture.md"))

		body, err := page.Locator(".page-view").InnerText()
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(ContainSubstring("Fixture table"))
		Expect(body).To(ContainSubstring("alpha"))

		obsidianHref, err := page.Locator(".obsidian-link").GetAttribute("href")
		Expect(err).NotTo(HaveOccurred())
		Expect(obsidianHref).To(HavePrefix("obsidian://open?"))
	})

	It("navigates to a wikilink's resolved target inside vault-ui", func() {
		page := newPage("/v/fixture/page/25%20Tasks/Card%20Page%20Fixture.md")
		defer func() { _ = page.Close() }()

		wikilink := page.Locator(".page-view a", playwright.PageLocatorOptions{HasText: "Link Target Fixture"})
		Eventually(func() int {
			count, err := wikilink.Count()
			Expect(err).NotTo(HaveOccurred())
			return count
		}, "10s", "200ms").Should(BeNumerically(">", 0))

		href, err := wikilink.First().GetAttribute("href")
		Expect(err).NotTo(HaveOccurred())
		Expect(href).NotTo(HavePrefix("obsidian://"))
		Expect(href).To(ContainSubstring("/page/25%20Tasks/Link%20Target%20Fixture.md"))

		resp, err := page.ExpectResponse(func(r playwright.Response) bool {
			return strings.Contains(r.URL(), "Link%20Target%20Fixture")
		}, func() error { return wikilink.First().Click() })
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Status()).To(Equal(200))
		Expect(page.URL()).To(ContainSubstring("/page/25%20Tasks/Link%20Target%20Fixture.md"))

		body, err := page.Locator(".page-view").InnerText()
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(ContainSubstring("Link target body marker"))
	})

	It("renders a page that carries no frontmatter", func() {
		page := newPage("/v/fixture/page/50%20Knowledge%20Base/No%20Frontmatter%20Fixture.md")
		defer func() { _ = page.Close() }()

		body, err := page.Locator(".page-view").InnerText()
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(ContainSubstring("No Frontmatter Fixture"))
		Expect(body).To(ContainSubstring("carries no frontmatter block"))
	})

	It("resolves a wikilink to a page in another folder", func() {
		page := newPage("/v/fixture/page/24%20Goals/Cross%20Folder%20Fixture.md")
		defer func() { _ = page.Close() }()

		link := page.Locator(".page-view a", playwright.PageLocatorOptions{HasText: "Card Page Fixture"})
		Eventually(func() int {
			count, err := link.Count()
			Expect(err).NotTo(HaveOccurred())
			return count
		}, "10s", "200ms").Should(BeNumerically(">", 0))

		href, err := link.First().GetAttribute("href")
		Expect(err).NotTo(HaveOccurred())
		Expect(href).To(ContainSubstring("/page/25%20Tasks/Card%20Page%20Fixture.md"))
	})

	It("lists a backlink from another folder", func() {
		page := newPage("/v/fixture/page/25%20Tasks/Card%20Page%20Fixture.md")
		defer func() { _ = page.Close() }()

		backlinks := page.Locator(".backlinks a", playwright.PageLocatorOptions{HasText: "Cross Folder Fixture"})
		Eventually(func() int {
			count, err := backlinks.Count()
			Expect(err).NotTo(HaveOccurred())
			return count
		}, "10s", "200ms").Should(BeNumerically(">", 0))
	})

	It("styles a rendered page so its links and headings read as such", func() {
		page := newPage("/v/fixture/page/25%20Tasks/Card%20Page%20Fixture.md")
		defer func() { _ = page.Close() }()

		link := page.Locator(".page-view a", playwright.PageLocatorOptions{HasText: "Link Target Fixture"})
		Eventually(func() int {
			count, err := link.Count()
			Expect(err).NotTo(HaveOccurred())
			return count
		}, "10s", "200ms").Should(BeNumerically(">", 0))

		// An href alone passes on an invisible link. The renderer emitted this
		// link all along; the sheet carried no page-view rule, so it painted in
		// the browser's default anchor colour on the #1a1a1a body. Assert the
		// computed style, not the markup.
		linkColour, err := link.First().Evaluate(`el => getComputedStyle(el).color`, nil)
		Expect(err).NotTo(HaveOccurred())

		paragraph := page.Locator(".page-view p").First()
		bodyColour, err := paragraph.Evaluate(`el => getComputedStyle(el).color`, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(linkColour).NotTo(Equal(bodyColour), "a link must not paint in the body colour")

		decoration, err := link.First().Evaluate(`el => getComputedStyle(el).textDecorationLine`, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoration).To(ContainSubstring("underline"))

		heading := page.Locator(".page-view h2", playwright.PageLocatorOptions{HasText: "Fixture table"})
		Eventually(func() int {
			count, err := heading.Count()
			Expect(err).NotTo(HaveOccurred())
			return count
		}, "10s", "200ms").Should(BeNumerically(">", 0))

		headingSize, err := heading.First().Evaluate(`el => parseFloat(getComputedStyle(el).fontSize)`, nil)
		Expect(err).NotTo(HaveOccurred())
		bodySize, err := paragraph.Evaluate(`el => parseFloat(getComputedStyle(el).fontSize)`, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(headingSize.(float64)).To(BeNumerically(">", bodySize.(float64)),
			"a heading must not render at body size")
	})
})
