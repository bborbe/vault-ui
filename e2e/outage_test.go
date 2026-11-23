// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Scenario: with the fixture vault-api up the board shows rows; with it down the
// board keeps its last-good rows and renders the "vault-api unavailable" banner.
var _ = Describe("Scenario: vault-api outage", func() {
	It("shows the unavailable banner and keeps the row count when the source is down", func() {
		// Up: the board serves the fixture rows from the stub vault-api.
		page := newPage("/")
		Eventually(func() int { return countOf(page, ".task-card") }, "20s", "500ms").
			Should(BeNumerically(">", 0), "the board fetches from the stub vault-api")
		rows := settledCount(page, ".task-card")
		Expect(rows).To(BeNumerically(">", 0))
		// The banner is absent while the source is healthy — the half that makes
		// the down-state assertion discriminating (a permanently-rendered banner
		// would otherwise pass).
		healthyBanner := page.Locator("[data-testid='vault-api-unavailable']")
		healthyVisible, err := healthyBanner.IsVisible()
		Expect(err).NotTo(HaveOccurred())
		Expect(healthyVisible).To(BeFalse(), "the banner is absent while vault-api is up")
		_ = page.Close()

		// Down: close the stub, and wait for the source's next poll to record it.
		// No deferred stopStub here: it ran after the restart below and left the
		// suite with no source for every spec that followed. The suite's
		// AfterEach restores the stub if this spec fails mid-outage.
		stopStub()
		Eventually(func() bool { return sourceUnavailable() }, "25s", "500ms").
			Should(BeTrue(), "the source polls every 10s; the outage registers on the next tick")

		// Reload: the banner is visible and the last-good rows are unchanged.
		downPage := newPage("/")
		banner := downPage.Locator("[data-testid='vault-api-unavailable']")
		Eventually(func() bool {
			visible, err := banner.IsVisible()
			return err == nil && visible
		}, "10s", "200ms").Should(BeTrue(), "the banner renders on a load during the outage")
		Eventually(func() int { return countOf(downPage, ".task-card") }, "10s", "300ms").
			Should(Equal(rows), "the board keeps its last-good rows")
		_ = downPage.Close()

		// Recovery: restart the stub. The next poll clears the flag, and a fresh
		// load renders the board with no banner — the "absent when up" half again,
		// this time after a real outage rather than on the first load.
		Expect(restartStub(context.Background())).To(Succeed(), "restart the fixture stub")
		Eventually(func() bool { return sourceUnavailable() }, "25s", "500ms").
			Should(BeFalse(), "the source recovers on the next poll after the stub restarts")
		recoveredPage := newPage("/")
		defer func() { _ = recoveredPage.Close() }()
		recoveredBanner := recoveredPage.Locator("[data-testid='vault-api-unavailable']")
		Eventually(func() bool {
			visible, err := recoveredBanner.IsVisible()
			return err == nil && !visible
		}, "10s", "200ms").Should(BeTrue(), "the banner is absent after the source recovers")
		Eventually(func() int { return countOf(recoveredPage, ".task-card") }, "20s", "500ms").
			Should(Equal(rows), "the rows are back after recovery")
	})
})

// sourceUnavailable reads GET /api/source-status.
func sourceUnavailable() bool {
	resp, err := http.Get(baseURL + "/api/source-status")
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Unavailable bool `json:"unavailable"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false
	}
	return body.Unavailable
}
