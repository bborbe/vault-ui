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
		rows := countOf(page, ".task-card")
		Expect(rows).To(BeNumerically(">", 0))
		_ = page.Close()

		// Down: close the stub, and wait for the source's next poll to record it.
		stopStub()
		defer func() {
			Expect(restartStub(context.Background())).To(Succeed(), "restart the fixture stub")
		}()
		Eventually(func() bool { return sourceUnavailable() }, "25s", "500ms").
			Should(BeTrue(), "the source polls every 10s; the outage registers on the next tick")

		// Reload: the banner is visible and the last-good rows are unchanged.
		page = newPage("/")
		defer func() { _ = page.Close() }()
		banner := page.Locator("[data-testid='vault-api-unavailable']")
		Eventually(func() bool {
			visible, err := banner.IsVisible()
			return err == nil && visible
		}, "10s", "200ms").Should(BeTrue(), "the banner renders on a load during the outage")
		Expect(countOf(page, ".task-card")).To(Equal(rows), "the board keeps its last-good rows")
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
