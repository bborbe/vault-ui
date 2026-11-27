// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build e2e

package e2e

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

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

		// Two base fixture topics: one unassigned, one assigned to e2e. The
		// deferred-visibility fixtures live in this folder too, so assert that
		// the view renders them rather than pinning a total every new fixture
		// would break.
		Eventually(func() int { return countOf(page, ".topic-card") }, "15s", "500ms").
			Should(BeNumerically(">=", 2))
		Eventually(func() int {
			return countOf(page, ".topic-card:has-text('Assigned Fixture Topic')")
		}, "10s", "200ms").Should(Equal(1))

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

	It("keeps the operator's status filter when switching views", func() {
		// setView() used to reset currentStatuses to a per-view default on every
		// tab switch, and updateURL() then persisted that reset. A filter that
		// excluded `next` gained it back on the way to Goals, so goals the
		// operator had filtered out reappeared in the view they had just chosen.
		// The filter belongs to the operator, not to the view.
		page := newPage("/?view=tasks&status=in_progress&status=hold&status=completed")
		defer func() { _ = page.Close() }()

		want := []string{"in_progress", "hold", "completed"}
		Eventually(func() int { return countOf(page, ".task-card") }, "15s", "500ms").
			Should(BeNumerically(">", 0))
		Expect(statusParams(page)).To(Equal(want))

		for _, view := range []string{"goals", "topics", "tasks"} {
			clickView(page, view)
			Eventually(func() []string { return statusParams(page) }, "5s", "200ms").
				Should(Equal(want), "switching to %s must not rewrite the status filter", view)
		}
	})

	It("lands on the Goals view without fetching /api/tasks first", func() {
		// Built by hand so the request handler is registered BEFORE the first
		// navigation, and asserted on the request rather than on
		// performance.getEntriesByType: a probe that forced every view through
		// loadTasks passed this spec when it read the performance buffer, so
		// that signal does not record the fetch this assertion is about.
		page, err := browser.NewPage()
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = page.Close() }()

		rec := &recorder{}
		page.OnRequest(func(req playwright.Request) {
			if strings.Contains(req.URL(), "/api/tasks") {
				rec.add(req.URL())
			}
		})

		_, err = page.Goto(baseURL+"/?view=goals", playwright.PageGotoOptions{
			WaitUntil: playwright.WaitUntilStateLoad,
		})
		Expect(err).NotTo(HaveOccurred())

		// Positive first: once goal cards have rendered, the fetch that would
		// have flashed Tasks has had its chance. Only then is the absence
		// meaningful.
		Eventually(func() int { return countOf(page, ".goal-card") }, "15s", "500ms").
			Should(BeNumerically(">", 0))

		Expect(rec.len()).To(Equal(0),
			"a deep link to ?view=goals must not fetch /api/tasks")
	})

	It("reaches the open board without a manual reload when the vault changes", func(ctx SpecContext) {
		page := newPage("/")
		defer func() { _ = page.Close() }()

		Eventually(func() int { return countOf(page, ".task-card") }, "15s", "500ms").
			Should(BeNumerically(">", 0))
		before := settledCount(page, ".task-card")

		Expect(writeFixtureTask(ctx, vaultPath, "Arrived During The Walk")).To(Succeed())

		Eventually(func() int { return countOf(page, ".task-card") }, "30s", "1s").
			Should(BeNumerically(">", before))
	})
})

var _ = Describe("Scenario 001: the console", func() {
	It("stays clean across a live-update window", func(ctx SpecContext) {
		// Built by hand rather than via newPage so the response handler is
		// registered before the first navigation — a handler attached after
		// Goto misses the very requests it exists to catch.
		page, err := browser.NewPage()
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = page.Close() }()

		var mu sync.Mutex
		var serverFailures []string
		var consoleErrors []string
		// A render exception inside a fetch handler is CAUGHT — it reaches the
		// toast path plus console.error, never pageerror. Without this handler
		// the assertion would pass on exactly the failure the gate exists for.
		page.OnConsole(func(msg playwright.ConsoleMessage) {
			if msg.Type() == "error" {
				mu.Lock()
				defer mu.Unlock()
				consoleErrors = append(consoleErrors, msg.Text())
			}
		})
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

		Eventually(func() int { return countOf(page, ".task-card") }, "15s", "500ms").
			Should(BeNumerically(">", 0))

		// The live stream reports itself connected by dropping `disconnected`
		// from the status dot. The old spec waited for two 60 s poll ticks; the
		// poll loop is gone, so the window is anchored on the stream instead.
		Eventually(func() string {
			class, err := page.Locator("#ws-status").GetAttribute("class")
			Expect(err).NotTo(HaveOccurred())
			return class
		}, "15s", "200ms").ShouldNot(ContainSubstring("disconnected"),
			"the SSE stream must connect")

		// Hold the window open across three 15 s heartbeats so a stream error or
		// a reconnect lands inside it.
		time.Sleep(45 * time.Second)

		// Positive control: a live event still reaches the board during that
		// window. Without it, a stream that silently died would pass the
		// cleanliness assertions above.
		before := settledCount(page, ".task-card")
		Expect(writeFixtureTask(ctx, vaultPath, "Arrived During The Console Walk")).To(Succeed())
		Eventually(func() int { return countOf(page, ".task-card") }, "30s", "1s").
			Should(BeNumerically(">", before), "a live event must still re-read the board")

		errs, err := page.PageErrors()
		Expect(err).NotTo(HaveOccurred())
		Expect(errs).To(BeEmpty(), "uncaught exceptions during the walk")

		mu.Lock()
		defer mu.Unlock()
		Expect(serverFailures).To(BeEmpty(), "5xx /api/ responses during the walk")
		Expect(consoleErrors).To(BeEmpty(),
			"console.error during the walk — a caught render exception lands here")
	})
})

// countOf counts elements matching sel.
func countOf(page playwright.Page, sel string) int {
	count, err := page.Locator(sel).Count()
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return count
}

// settledCount returns the element count once it has stopped changing. The
// board fetches from vault-api client-side, so a count read straight after a
// navigation is routinely 0 or partial — waiting only for "> 0" returns at
// whatever intermediate number happened to be on screen. Every assertion that
// treats a count as a fixed baseline goes through here.
func settledCount(page playwright.Page, sel string) int {
	prev := -1
	EventuallyWithOffset(1, func() bool {
		now := countOf(page, sel)
		settled := now == prev && now > 0
		prev = now
		return settled
	}, "20s", "400ms").Should(BeTrue(), "the count of %s never settled above zero", sel)
	return prev
}

// statusParams returns the status query parameters in URL order.
func statusParams(page playwright.Page) []string {
	parsed, err := url.Parse(page.URL())
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return parsed.Query()["status"]
}

// clickView clicks a view toggle.
func clickView(page playwright.Page, view string) {
	ExpectWithOffset(1,
		page.Locator(".view-toggle-btn[data-view='"+view+"']").First().Click(),
	).To(Succeed())
}
