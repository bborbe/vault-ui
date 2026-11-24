// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageview_test

import (
	"context"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/pageview"
	"github.com/bborbe/vault-ui/pkg/vaultapi"
)

// recordedCall is one read the recording source received.
type recordedCall struct {
	method   string
	vault    string
	pagePath string
}

// recordingSource records every Page and Backlinks call so a spec can assert
// the exact read set a render costs.
type recordingSource struct {
	calls     []recordedCall
	page      *vaultapi.Page
	backlinks []vaultapi.Backlink
}

func (r *recordingSource) Page(
	_ context.Context,
	vaultName string,
	pagePath string,
) (*vaultapi.Page, error) {
	r.calls = append(r.calls, recordedCall{"Page", vaultName, pagePath})
	return r.page, nil
}

func (r *recordingSource) Backlinks(
	_ context.Context,
	vaultName string,
	pagePath string,
) ([]vaultapi.Backlink, error) {
	r.calls = append(r.calls, recordedCall{"Backlinks", vaultName, pagePath})
	return r.backlinks, nil
}

var _ = Describe("The vault-service seam", func() {
	It("makes exactly one Page and one Backlinks call per render", func() {
		source := &recordingSource{
			page: &vaultapi.Page{
				Name:    "Alpha",
				Content: "# Alpha\n\nSee [[Beta]].\n",
				Links:   map[string]string{"Beta": "Beta.md"},
				Embeds:  map[string]string{},
			},
			backlinks: []vaultapi.Backlink{{Path: "Links.md", Name: "Links"}},
		}
		ctx := context.Background()

		first := pageview.NewService(fixtureVaults{}, source)
		_, err := first.Render(ctx, vaultName, "Alpha.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(source.calls).To(Equal([]recordedCall{
			{"Page", vaultName, "Alpha.md"},
			{"Backlinks", vaultName, "Alpha.md"},
		}))

		// A second, freshly constructed service: the page view keeps no index, so
		// a cold service makes the same two reads. A second cold service is what
		// makes "no rebuild on demand" testable without sleeping past the old
		// 30 s TTL.
		source.calls = nil
		second := pageview.NewService(fixtureVaults{}, source)
		_, err = second.Render(ctx, vaultName, "Alpha.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(source.calls).To(Equal([]recordedCall{
			{"Page", vaultName, "Alpha.md"},
			{"Backlinks", vaultName, "Alpha.md"},
		}))
	})

	// Reading the source is the only way to catch a direct call that bypasses
	// the seam: a stray os.ReadFile or whole-vault walk would still compile and
	// still pass every behavioural spec.
	It("carries no whole-vault walk, file read or local backlink scan", func() {
		source, err := os.ReadFile("pageview.go")
		Expect(err).NotTo(HaveOccurred())
		for _, forbidden := range []string{
			"filepath.WalkDir",
			"os.ReadFile",
			"markdown.Backlinks",
			"indexTTL",
		} {
			Expect(string(source)).NotTo(ContainSubstring(forbidden))
		}
	})
})
