// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build e2e

package e2e

import (
	"github.com/mxschmitt/playwright-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The deferred-visibility rule must read the same in all three views. The live
// vault carries no deferred topics, so the assertion runs against the seeded
// fixtures: inside-window present and marked `upcoming`, past present and not,
// far-future absent, undated present and not.
//
// The fixtures are not torn down here: other specs assert against the same
// vault, and Ginkgo orders specs across files arbitrarily. The teardown is
// proved in TestDeferredFixturesAreRemovable (harness_test.go), which runs
// without a browser and on its own temp vault.
var _ = Describe("deferred visibility across the three views", func() {
	views := []struct{ url, card string }{
		{"/?view=tasks", ".task-card"},
		{"/?view=goals", ".goal-card"},
		{"/?view=topics", ".topic-card"},
	}

	for _, view := range views {
		view := view
		It("applies the four cases in "+view.url, func() {
			page := newPage(view.url)
			defer func() { _ = page.Close() }()

			Eventually(func() int { return countOf(page, view.card) }, "15s", "500ms").
				Should(BeNumerically(">", 0), "the board fetches asynchronously")

			card := func(title string) playwright.Locator {
				return page.Locator(view.card, playwright.PageLocatorOptions{HasText: title})
			}

			inside := card("Defer Fixture Inside Window")
			Eventually(func() int {
				count, err := inside.Count()
				Expect(err).NotTo(HaveOccurred())
				return count
			}, "10s", "200ms").Should(Equal(1), "a defer inside the window is visible")
			insideClass, err := inside.GetAttribute("class")
			Expect(err).NotTo(HaveOccurred())
			Expect(insideClass).To(ContainSubstring("upcoming"), "an in-window defer is marked upcoming")

			past := card("Defer Fixture Past")
			Expect(past.Count()).To(Equal(1), "a past defer is visible")
			pastClass, err := past.GetAttribute("class")
			Expect(err).NotTo(HaveOccurred())
			Expect(pastClass).NotTo(ContainSubstring("upcoming"), "a past defer is not upcoming")

			undated := card("Defer Fixture Undated")
			Expect(undated.Count()).To(Equal(1), "an undated row is visible")

			far := card("Defer Fixture Far Future")
			Expect(far.Count()).To(Equal(0), "a defer beyond the window is hidden")
		})
	}
})
