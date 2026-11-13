// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build e2e

package e2e

import (
	"regexp"
	"strings"
	"sync"

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
		checkStubs := stubSessionRoutes(page)

		rec := &recorder{}
		page.OnRequest(func(req playwright.Request) {
			if runRoutePattern.MatchString(req.URL()) {
				rec.add(req.URL())
			}
		})

		start := page.Locator(".start-btn").First()
		label, err := start.InnerText()
		Expect(err).NotTo(HaveOccurred())
		Expect(label).To(ContainSubstring("Start"),
			"the control reads Start; there is no Run button")

		Expect(start.Click()).To(Succeed())
		Eventually(func() int { return rec.len() }, "10s", "200ms").Should(BeNumerically(">", 0))
		Expect(rec.first()).To(ContainSubstring("/run"))
		checkStubs()
	})

	It("renders no jump control on a sessionless task", func() {
		page := newPage("/")
		defer func() { _ = page.Close() }()
		checkStubs := stubSessionRoutes(page)

		card := page.Locator(".task-card").Filter(
			playwright.LocatorFilterOptions{HasText: "Sessionless Fixture Task"})
		Eventually(func() int {
			count, err := card.Count()
			if err != nil {
				return 0
			}
			return count
		}, "15s", "500ms").Should(Equal(1))

		jumps, err := card.Locator(".jump-btn").Count()
		Expect(err).NotTo(HaveOccurred())
		Expect(jumps).To(Equal(0),
			"app.js renders the jump control only for a live task with a resolved pane")
		checkStubs()
	})

	It("does not fabricate a jump control from a stubbed run", func() {
		page := newPage("/")
		defer func() { _ = page.Close() }()
		checkStubs := stubSessionRoutes(page)

		Eventually(func() int { return countOf(page, ".start-btn") }, "15s", "500ms").
			Should(BeNumerically(">", 0))
		Expect(page.Locator(".start-btn").First().Click()).To(Succeed())

		// The stub answers 200 but creates no session and resolves no pane, so
		// the board must not start offering a jump target it does not have.
		Consistently(func() int {
			return countOf(page, ".jump-btn")
		}, "3s", "500ms").Should(Equal(0))
		checkStubs()
	})

	It("does not claim a session it did not create", func() {
		page := newPage("/")
		defer func() { _ = page.Close() }()
		checkStubs := stubSessionRoutes(page)

		rec := &recorder{}
		page.OnRequest(func(req playwright.Request) {
			if runRoutePattern.MatchString(req.URL()) {
				rec.add(req.URL())
			}
		})

		Eventually(func() int { return countOf(page, ".start-btn") }, "15s", "500ms").
			Should(BeNumerically(">", 0))
		Expect(page.Locator(".start-btn").First().Click()).To(Succeed())
		Eventually(func() int { return rec.len() }, "10s", "200ms").Should(BeNumerically(">=", 1))

		// A stubbed 200 reports success but starts nothing, so a live badge
		// here is a shown-versus-actual defect, not a slow start.
		Expect(countOf(page, ".live-badge")).To(Equal(0),
			"a stubbed run must not render a live session")
		checkStubs()
	})

	It("renders every card with a title", func() {
		page := newPage("/")
		defer func() { _ = page.Close() }()

		Eventually(func() int { return countOf(page, ".task-card") }, "15s", "500ms").
			Should(BeNumerically(">", 0))

		cards := page.Locator(".task-card")
		total, err := cards.Count()
		Expect(err).NotTo(HaveOccurred())
		for i := 0; i < total; i++ {
			title, err := cards.Nth(i).InnerText()
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(title)).NotTo(BeEmpty(),
				"a card rendered with no text is a rendering defect")
		}
	})
})

// recorder collects strings from a Playwright dispatch goroutine. Playwright
// runs callbacks off the test goroutine, so the slice needs its own lock —
// and no Ginkgo assertion may be made from inside a callback.
type recorder struct {
	mu    sync.Mutex
	items []string
}

// add appends one value under the lock.
func (r *recorder) add(value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, value)
}

// len reports how many values were recorded.
func (r *recorder) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.items)
}

// first returns the first recorded value, or "".
func (r *recorder) first() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.items) == 0 {
		return ""
	}
	return r.items[0]
}

// joined renders the recorded values for a failure message.
func (r *recorder) joined() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.items, "; ")
}

// stubSessionRoutes fulfils the run and jump routes so the suite never spawns a
// real session — that keeps `vault-cli` and `claude` off the harness's
// requirements and leaves nothing for AfterSuite to clean up. It returns a
// checker for any fulfil error, because a Ginkgo assertion cannot be made from
// inside the Playwright callback.
func stubSessionRoutes(page playwright.Page) func() {
	errs := &recorder{}
	fulfil := func(route playwright.Route) {
		if err := route.Fulfill(playwright.RouteFulfillOptions{
			Status:      playwright.Int(200),
			ContentType: playwright.String("application/json"),
			Body:        playwright.String(`{"ok":true}`),
		}); err != nil {
			errs.add(err.Error())
		}
	}
	ExpectWithOffset(1, page.Route(runRoutePattern, fulfil)).To(Succeed())
	ExpectWithOffset(1, page.Route(jumpRoutePattern, fulfil)).To(Succeed())

	return func() {
		ExpectWithOffset(1, errs.len()).To(Equal(0),
			"route fulfil failed: %s", errs.joined())
	}
}
