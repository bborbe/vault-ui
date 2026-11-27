// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageview_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/board"
	"github.com/bborbe/vault-ui/pkg/openable"
	"github.com/bborbe/vault-ui/pkg/pageview"
)

// folderVaults configures the task and goal folders the folder fixture uses, so
// the page view can tell a task page from a goal page.
type folderVaults struct{ dir string }

func (v folderVaults) Vaults(context.Context) ([]board.Vault, error) {
	return []board.Vault{{
		Name:        vaultName,
		VaultName:   vaultName,
		Path:        v.dir,
		TasksFolder: "25 Tasks",
		GoalsFolder: "24 Goals",
	}}, nil
}

type mapHolds map[string]*api.HoldResponse

func (m mapHolds) Hold(vault, kind, name string) *api.HoldResponse {
	return m[openable.Key(vault, kind, name)]
}

func (m mapHolds) Generation() uint64 { return 0 }

var _ = Describe("Page view hold reason", func() {
	summary := `re-check <after> deploy`
	prURL := "https://github.com/bborbe/x/pull/1"
	holds := mapHolds{
		openable.Key(vaultName, "task", "Cross Folder Task"): {
			Reason: "parked-on-pr", Label: "🔗 waiting on PR", Summary: &summary, PRURL: &prURL,
			Blockers: []string{},
		},
	}

	newHoldService := func(lookup openable.Lookup) pageview.Service {
		dir := newFolderFixture(GinkgoT())
		return pageview.NewServiceWithHolds(folderVaults{dir: dir}, newFixtureSource(GinkgoT(), dir), lookup)
	}

	It("renders the badge, escaped summary and PR link on a held task page", func() {
		got, err := newHoldService(holds).Render(context.Background(), vaultName, "25 Tasks/Cross Folder Task.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring(`class="page-hold hold-reason"`))
		Expect(got).To(ContainSubstring("🔗 waiting on PR"))
		Expect(got).To(ContainSubstring("re-check &lt;after&gt; deploy"))
		Expect(got).To(ContainSubstring(`href="https://github.com/bborbe/x/pull/1"`))
	})

	It("renders no badge for a page whose kind does not match the hold", func() {
		got, err := newHoldService(holds).Render(context.Background(), vaultName, "24 Goals/Cross Folder Goal.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(ContainSubstring("page-hold"))
	})

	It("drops a non-http PR link", func() {
		bad := "javascript:alert(1)"
		lookup := mapHolds{
			openable.Key(vaultName, "task", "Cross Folder Task"): {
				Reason: "parked-on-pr", Label: "🔗 waiting on PR", PRURL: &bad, Blockers: []string{},
			},
		}
		got, err := newHoldService(lookup).Render(context.Background(), vaultName, "25 Tasks/Cross Folder Task.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring("🔗 waiting on PR"))
		Expect(got).NotTo(ContainSubstring("hold-reason-pr"))
	})

	It("renders no badge without a lookup", func() {
		got, err := newHoldService(nil).Render(context.Background(), vaultName, "25 Tasks/Cross Folder Task.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(ContainSubstring("page-hold"))
	})
})
