// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build e2e

package e2e

import (
	"regexp"
	"strings"

	"github.com/mxschmitt/playwright-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The board posts to /api/tasks/{id}/run?vault=… — a query-bearing URL. A glob
// like "**/api/tasks/*/run" does not match the query string, so the stub would
// never engage and the click would reach the real handler. A regexp is what
// actually matches.
var (
	runRoutePattern  = regexp.MustCompile(`.*/api/tasks/[^/]+/run.*`)
	jumpRoutePattern = regexp.MustCompile(`.*/api/tasks/[^/]+/jump.*`)
)

var _ = Describe("Scenario 002: running and jumping to a task session", func() {
	It("renders ▶ Start and posts to the run route", func() {
		page := newPage("/")
		defer func() { _ = page.Close() }()
		stubSessionRoutes(page)

		var posted []string
		page.OnRequest(func(req playwright.Request) {
			if runRoutePattern.MatchString(req.URL()) {
				posted = append(posted, req.URL())
			}
		})

		start := page.Locator(".start-btn").First()
		label, err := start.InnerText()
		Expect(err).NotTo(HaveOccurred())
		Expect(label).To(ContainSubstring("Start"),
			"the control reads Start; there is no Run button")

		Expect(start.Click()).To(Succeed())
		Eventually(func() int { return len(posted) }, "10s", "200ms").Should(BeNumerically(">", 0))
		Expect(posted[0]).To(ContainSubstring("/run"))
	})

	It("renders no jump control on a sessionless task", func() {
		page := newPage("/")
		defer func() { _ = page.Close() }()
		stubSessionRoutes(page)

		card := page.Locator(".task-card").Filter(
			playwright.LocatorFilterOptions{HasText: "Sessionless Fixture Task"})
		count, err := card.Count()
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(Equal(1))

		jumps, err := card.Locator(".jump-btn").Count()
		Expect(err).NotTo(HaveOccurred())
		Expect(jumps).To(Equal(0),
			"app.js renders the jump control only for a live task with a resolved pane")
	})

	It("does not fabricate a jump control from a stubbed run", func() {
		page := newPage("/")
		defer func() { _ = page.Close() }()
		stubSessionRoutes(page)

		Expect(page.Locator(".start-btn").First().Click()).To(Succeed())

		// The stub answers 200 but creates no session and resolves no pane, so
		// the board must not start offering a jump target it does not have.
		Consistently(func() int {
			return countOf(page, ".jump-btn")
		}, "3s", "500ms").Should(Equal(0))
	})

	It("does not claim a session it did not create", func() {
		page := newPage("/")
		defer func() { _ = page.Close() }()
		stubSessionRoutes(page)

		var posted int
		page.OnRequest(func(req playwright.Request) {
			if runRoutePattern.MatchString(req.URL()) {
				posted++
			}
		})

		start := page.Locator(".start-btn").First()
		Expect(start.Click()).To(Succeed())
		Eventually(func() int { return posted }, "10s", "200ms").Should(BeNumerically(">=", 1))

		// A stubbed 200 reports success but starts nothing, so a live badge
		// here is a shown-versus-actual defect, not a slow start.
		Expect(countOf(page, ".live-badge")).To(Equal(0),
			"a stubbed run must not render a live session")
	})

	It("renders every card with a title", func() {
		page := newPage("/")
		defer func() { _ = page.Close() }()

		cards := page.Locator(".task-card")
		total, err := cards.Count()
		Expect(err).NotTo(HaveOccurred())
		Expect(total).To(BeNumerically(">", 0))

		for i := 0; i < total; i++ {
			title, err := cards.Nth(i).InnerText()
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(title)).NotTo(BeEmpty(),
				"a card rendered with no text is a rendering defect")
		}
	})
})

// stubSessionRoutes fulfils the run and jump routes so the suite never spawns a
// real session — that keeps `vault-cli` and `claude` off the harness's
// requirements and leaves nothing for AfterSuite to clean up.
func stubSessionRoutes(page playwright.Page) {
	fulfil := func(route playwright.Route) {
		Expect(route.Fulfill(playwright.RouteFulfillOptions{
			Status:      playwright.Int(200),
			ContentType: playwright.String("application/json"),
			Body:        playwright.String(`{"ok":true}`),
		})).To(Succeed())
	}
	ExpectWithOffset(1, page.Route(runRoutePattern, fulfil)).To(Succeed())
	ExpectWithOffset(1, page.Route(jumpRoutePattern, fulfil)).To(Succeed())
}
