// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build e2e

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Scenario: a vault the board lists but cannot serve is marked as such, while a
// served vault that merely has no visible rows is not.
var _ = Describe("Scenario: no-source marker", func() {
	It("marks the unserved vault and leaves the served vault unmarked", func() {
		unserved := newPage("/?vault=unserved")
		defer func() { _ = unserved.Close() }()
		marker := unserved.Locator("[data-testid='vault-no-source']")
		Eventually(func() bool {
			visible, err := marker.IsVisible()
			return err == nil && visible
		}, "15s", "200ms").Should(BeTrue(), "the unserved vault shows the no-source marker")

		// A served vault whose status filter matches no fixture renders the
		// ordinary empty board — and must not be mistaken for an unserved one.
		served := newPage("/?vault=fixture&status=aborted")
		defer func() { _ = served.Close() }()
		Eventually(func() int {
			return countOf(served, "#vault-selector-dropdown input[value='fixture']")
		}, "15s", "200ms").Should(Equal(1), "the board has loaded its vault list")
		Expect(countOf(served, ".task-card")).To(Equal(0),
			"the status filter hides every fixture task")
		servedMarker := served.Locator("[data-testid='vault-no-source']")
		visible, err := servedMarker.IsVisible()
		Expect(err).NotTo(HaveOccurred())
		Expect(visible).To(BeFalse(), "a served vault with no rows is not unserved")
	})

	It("does not raise the marker in the all-vaults view", func() {
		// The default view has no selection to report on. Marking every unserved
		// vault there is the false positive this rule exists to prevent, so the
		// marker must stay hidden even though the fixture config lists one.
		all := newPage("/")
		defer func() { _ = all.Close() }()
		Eventually(func() int {
			return countOf(all, "#vault-selector-dropdown input[value='fixture']")
		}, "15s", "200ms").Should(Equal(1), "the board has loaded its vault list")

		visible, err := all.Locator("[data-testid='vault-no-source']").IsVisible()
		Expect(err).NotTo(HaveOccurred())
		Expect(visible).To(BeFalse(), "the all-vaults view raises no no-source marker")
	})
})
