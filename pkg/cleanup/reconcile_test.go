// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cleanup_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/cleanup"
	"github.com/bborbe/vault-ui/pkg/session"
)

// scanner returns a ProcessScanner serving a fixed ps table.
func scanner(output string) session.ProcessScanner {
	return func(context.Context) (string, error) { return output, nil }
}

func reconcile(f *sweepFixture) int {
	cleared, err := f.sweep().ReconcileOrphanedMarkers(context.Background())
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return cleared
}

var _ = Describe("ReconcileOrphanedMarkers", func() {
	It("clears a marker when the launch process is gone", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{
			ID: "t", Title: "T", ClaudeSessionID: testUUID,
			ClaudeSessionStarted: markerOlderThanTTL,
		}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		f.params.ProcessScanner = scanner("")

		Expect(reconcile(f)).To(Equal(1))
		clears := f.ops.callsOf("clearTask")
		Expect(clears).To(HaveLen(1))
		Expect(clears[0].key).To(Equal("claude_session_started"))
		_, present := f.reg.State(f.vault.Name, task.ID)
		Expect(present).To(BeFalse())
	})

	It("keeps the marker while a launch for the item runs here", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{
			ID: "t", Title: "T", ClaudeSessionID: testUUID,
			ClaudeSessionStarted: markerOlderThanTTL,
		}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		f.params.ProcessScanner = scanner("1234 claude --session-id " + testUUID)

		Expect(reconcile(f)).To(Equal(0))
		Expect(f.ops.callsOf("clearTask")).To(BeEmpty())
	})

	It("keeps a young marker", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{
			ID: "t", Title: "T", ClaudeSessionID: testUUID,
			ClaudeSessionStarted: markerWithinOrphanGrace,
		}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		f.params.ProcessScanner = scanner("")

		Expect(reconcile(f)).To(Equal(0))
		Expect(f.ops.callsOf("clearTask")).To(BeEmpty())
	})

	It("keeps the marker when the registry already holds a record", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{
			ID: "t", Title: "T", ClaudeSessionID: testUUID,
			ClaudeSessionStarted: markerOlderThanTTL,
		}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		f.params.ProcessScanner = scanner("")
		f.reg.Begin(f.vault.Name, task.ID, "task")

		Expect(reconcile(f)).To(Equal(0))
		Expect(f.ops.callsOf("clearTask")).To(BeEmpty())
	})

	It("treats an unparseable legacy marker as expired and clears it", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{
			ID: "t", Title: "T", ClaudeSessionID: testUUID,
			ClaudeSessionStarted: "true",
		}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		f.params.ProcessScanner = scanner("")

		Expect(reconcile(f)).To(Equal(1))
	})

	It("keeps the marker when the clear fails", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{
			ID: "t", Title: "T", ClaudeSessionID: testUUID,
			ClaudeSessionStarted: markerOlderThanTTL,
		}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		f.params.ProcessScanner = scanner("")
		f.ops.clearTaskErr = context.DeadlineExceeded

		Expect(reconcile(f)).To(Equal(0))
	})
})
