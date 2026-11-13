// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"sync"

	"github.com/mxschmitt/playwright-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Scenario 001: the three views, their filters, and the URL", func() {
	It("renders the Tasks view by default and encodes the view in the URL", func() {
		page := newPage("/")
		defer func() { _ = page.Close() }()

		Eventually(func() int { return countOf(page, ".task-card") }, "15s", "500ms").
			Should(BeNumerically(">", 0), "the board fetches asynchronously")

		for _, view := range []string{"goals", "topics", "tasks"} {
			clickView(page, view)
			// The board writes other params first (status, sort), so the view
			// is a mid-query param — asserting "?view=" would only match when
			// it happened to come first.
			Expect(page.URL()).To(ContainSubstring("view=" + view))
		}
	})

	It("offers a mutating menu on goal cards", func() {
		page := newPage("/?view=goals")
		defer func() { _ = page.Close() }()

		Eventually(func() int { return countOf(page, ".goal-card") }, "15s", "500ms").
			Should(BeNumerically(">", 0))
		Expect(page.Locator(".goal-card .menu-btn").First().Click()).To(Succeed())

		menu := page.Locator(".task-menu")
		Eventually(func() int { return countOf(page, ".task-menu") }, "5s", "200ms").
			Should(Equal(1))
		text, err := menu.InnerText()
		Expect(err).NotTo(HaveOccurred())
		for _, label := range []string{"Complete Goal", "Defer Goal", "Abort Goal"} {
			Expect(text).To(ContainSubstring(label))
		}
	})

	It("applies the assignee filter to the Topics view too", func() {
		page := newPage("/?view=topics")
		defer func() { _ = page.Close() }()

		// Two fixture topics: one unassigned, one assigned to e2e.
		Eventually(func() int { return countOf(page, ".topic-card") }, "15s", "500ms").
			Should(Equal(2))

		// The filter is applied through the URL the board reads on load rather
		// than through the dropdown: the dropdown's option set comes from
		// /api/assignees, which is a separate concern from whether the filter
		// narrows this view. Both routes set the same `assignee` state, and
		// this one fails if matchesAssigneeSelection stops reaching Topics.
		filtered := newPage("/?view=topics&assignee=e2e")
		defer func() { _ = filtered.Close() }()

		Eventually(func() int { return countOf(filtered, ".topic-card") }, "15s", "500ms").
			Should(Equal(1),
				"the assignee filter narrows Topics client-side; two cards here is the defect")
	})

	It("restores the view and filters on reload", func() {
		page := newPage("/?view=goals&assignee=e2e")
		defer func() { _ = page.Close() }()

		Expect(page.URL()).To(ContainSubstring("view=goals"))
		Expect(page.URL()).To(ContainSubstring("assignee=e2e"))
		Eventually(func() int { return countOf(page, ".task-card") }, "15s", "500ms").
			Should(Equal(0), "the Goals view renders no task cards")
	})

	It("lands on the Goals view without fetching /api/tasks first", func() {
		page := newPage("/?view=goals")
		defer func() { _ = page.Close() }()

		raw, err := page.Evaluate(
			"performance.getEntriesByType('resource').map(e => e.name).join(',')", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Contains(raw.(string), "/api/tasks")).To(BeFalse(),
			"a deep link to ?view=goals must not fetch /api/tasks")
	})

	It("reaches the open board without a manual reload when the vault changes", func(ctx SpecContext) {
		page := newPage("/")
		defer func() { _ = page.Close() }()

		Eventually(func() int { return countOf(page, ".task-card") }, "15s", "500ms").
			Should(BeNumerically(">", 0))
		before := countOf(page, ".task-card")

		Expect(writeFixtureTask(ctx, vaultPath, "Arrived During The Walk")).To(Succeed())

		Eventually(func() int { return countOf(page, ".task-card") }, "30s", "1s").
			Should(BeNumerically(">", before))
	})
})

var _ = Describe("Scenario 001: the console", func() {
	It("stays clean across two poll intervals", func() {
		// Built by hand rather than via newPage so the response handler is
		// registered before the first navigation — a handler attached after
		// Goto misses the very requests it exists to catch.
		page, err := browser.NewPage()
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = page.Close() }()

		var mu sync.Mutex
		var serverFailures []string
		page.OnResponse(func(resp playwright.Response) {
			if resp.Status() >= 500 && strings.Contains(resp.URL(), "/api/") {
				mu.Lock()
				defer mu.Unlock()
				serverFailures = append(serverFailures,
					fmt.Sprintf("%d %s", resp.Status(), resp.URL()))
			}
		})

		_, err = page.Goto(baseURL+"/", playwright.PageGotoOptions{
			WaitUntil: playwright.WaitUntilStateLoad,
		})
		Expect(err).NotTo(HaveOccurred())

		// The poll interval is 60 s. Waiting past two of them is what stops a
		// poll-loop exception hiding behind a window where the poll never ran.
		Eventually(func() int {
			return pollTicks(page)
		}, "150s", "5s").Should(BeNumerically(">=", 2))

		errs, err := page.PageErrors()
		Expect(err).NotTo(HaveOccurred())
		Expect(errs).To(BeEmpty(), "uncaught exceptions during the walk")

		mu.Lock()
		defer mu.Unlock()
		Expect(serverFailures).To(BeEmpty(), "5xx /api/ responses during the walk")
	})
})

// countOf counts elements matching sel.
func countOf(page playwright.Page, sel string) int {
	count, err := page.Locator(sel).Count()
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return count
}

// clickView clicks a view toggle.
func clickView(page playwright.Page, view string) {
	ExpectWithOffset(1,
		page.Locator(".view-toggle-btn[data-view='"+view+"']").First().Click(),
	).To(Succeed())
}


// pollTicks counts the poll loop's own console line.
func pollTicks(page playwright.Page) int {
	messages, err := page.ConsoleMessages()
	if err != nil {
		return 0
	}
	ticks := 0
	for _, message := range messages {
		if strings.Contains(message.Text(), "Polling for updates") {
			ticks++
		}
	}
	return ticks
}
