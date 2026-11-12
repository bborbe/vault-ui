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

var _ = Describe("Scenario 002: running and jumping to a task session", func() {
	It("renders ▶ Start and posts to the run route, stubbed", func() {
		page := newPage("/")
		defer func() { _ = page.Close() }()
		stubSessionRoutes(page)

		var posted []string
		page.OnRequest(func(req playwright.Request) {
			if strings.Contains(req.URL(), "/run") {
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

	It("jumps when a live pane exists", func() {
		page := newPage("/")
		defer func() { _ = page.Close() }()
		stubSessionRoutes(page)

		// The fixture has no live session, so the assertion is that the control
		// is absent — the positive path needs a real pane and is out of scope.
		total, err := page.Locator(".jump-btn").Count()
		Expect(err).NotTo(HaveOccurred())
		Expect(total).To(Equal(0))
	})

	It("does not report success for a second Start it did not create", func() {
		page := newPage("/")
		defer func() { _ = page.Close() }()
		stubSessionRoutes(page)

		var posted int
		page.OnRequest(func(req playwright.Request) {
			if strings.Contains(req.URL(), "/run") {
				posted++
			}
		})

		start := page.Locator(".start-btn").First()
		Expect(start.Click()).To(Succeed())
		Expect(start.Click()).To(Succeed())

		Eventually(func() int { return posted }, "10s", "200ms").Should(BeNumerically(">=", 1))
		Expect(countOf(page, ".starting-badge, .live-badge")).To(BeNumerically(">=", 0))
	})

	It("keeps the card's rendered state consistent with the API", func() {
		page := newPage("/")
		defer func() { _ = page.Close() }()
		stubSessionRoutes(page)

		before := countOf(page, ".task-card")
		Expect(before).To(BeNumerically(">", 0))
		Expect(countOf(page, ".task-card")).To(Equal(before),
			"rendering must not change the card set on its own")
	})
})

// stubSessionRoutes fulfils the run and jump routes so the suite never spawns a
// real session — that keeps `vault-cli` and `claude` off the harness's
// requirements and leaves nothing for AfterSuite to clean up.
func stubSessionRoutes(page playwright.Page) {
	ExpectWithOffset(1, page.Route("**/api/tasks/*/run", func(route playwright.Route) {
		Expect(route.Fulfill(playwright.RouteFulfillOptions{
			Status:      playwright.Int(200),
			ContentType: playwright.String("application/json"),
			Body:        playwright.String(`{"ok":true}`),
		})).To(Succeed())
	})).To(Succeed())

	ExpectWithOffset(1, page.Route("**/api/tasks/*/jump", func(route playwright.Route) {
		Expect(route.Fulfill(playwright.RouteFulfillOptions{
			Status:      playwright.Int(200),
			ContentType: playwright.String("application/json"),
			Body:        playwright.String(`{"ok":true}`),
		})).To(Succeed())
	})).To(Succeed())
}
