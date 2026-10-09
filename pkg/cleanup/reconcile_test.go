// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cleanup_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/cleanup"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
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

// reconcileTask is the task the reconcile specs operate on. It carries a session
// id so the live-launch process check is exercised.
func reconcileTask() cleanup.Item {
	return cleanup.Item{ID: "t", Title: "T", ClaudeSessionID: testUUID}
}

// seedReconcileMarker seeds the marker in the status cache — the only source the
// reconcile pass reads (the vault-cli list does not emit the field).
func seedReconcileMarker(f *sweepFixture, vaultPath, marker string) {
	seedMarker(f.cache, f.vault.Name, vaultPath, "24 Tasks", reconcileTask().ID, marker)
}

var _ = Describe("ReconcileOrphanedMarkers", func() {
	It("clears a marker when the launch process is gone", func() {
		vaultPath := GinkgoT().TempDir()
		task := reconcileTask()
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		seedReconcileMarker(f, vaultPath, markerOlderThanTTL)
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
		f := newFixture([]cleanup.Item{reconcileTask()}, nil, vaultPath, nil)
		seedReconcileMarker(f, vaultPath, markerOlderThanTTL)
		f.params.ProcessScanner = scanner("1234 claude --session-id " + testUUID)

		Expect(reconcile(f)).To(Equal(0))
		Expect(f.ops.callsOf("clearTask")).To(BeEmpty())
	})

	It("keeps a young marker", func() {
		vaultPath := GinkgoT().TempDir()
		f := newFixture([]cleanup.Item{reconcileTask()}, nil, vaultPath, nil)
		seedReconcileMarker(f, vaultPath, markerWithinOrphanGrace)
		f.params.ProcessScanner = scanner("")

		Expect(reconcile(f)).To(Equal(0))
		Expect(f.ops.callsOf("clearTask")).To(BeEmpty())
	})

	It("keeps the marker when the registry already holds a record", func() {
		vaultPath := GinkgoT().TempDir()
		task := reconcileTask()
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		seedReconcileMarker(f, vaultPath, markerOlderThanTTL)
		f.params.ProcessScanner = scanner("")
		f.reg.Begin(f.vault.Name, task.ID, "task")

		Expect(reconcile(f)).To(Equal(0))
		Expect(f.ops.callsOf("clearTask")).To(BeEmpty())
	})

	It("treats an unparseable legacy marker as expired and clears it", func() {
		vaultPath := GinkgoT().TempDir()
		f := newFixture([]cleanup.Item{reconcileTask()}, nil, vaultPath, nil)
		seedReconcileMarker(f, vaultPath, "true")
		f.params.ProcessScanner = scanner("")

		Expect(reconcile(f)).To(Equal(1))
	})

	It("keeps the marker when the clear fails", func() {
		vaultPath := GinkgoT().TempDir()
		f := newFixture([]cleanup.Item{reconcileTask()}, nil, vaultPath, nil)
		seedReconcileMarker(f, vaultPath, markerOlderThanTTL)
		f.params.ProcessScanner = scanner("")
		f.ops.clearTaskErr = context.DeadlineExceeded

		Expect(reconcile(f)).To(Equal(0))
	})

	It("restores the marker after ReconcileOrphanedMarkers when a relaunch lands in the await", func() {
		vaultPath := GinkgoT().TempDir()
		task := reconcileTask()
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		seedReconcileMarker(f, vaultPath, markerOlderThanTTL)
		f.params.ProcessScanner = scanner("")
		f.ops.onClearTask = func() {
			f.ops.onClearTask = nil // the relaunch lands only once
			f.reg.Begin(f.vault.Name, task.ID, "task")
		}

		Expect(reconcile(f)).To(Equal(0))
		Expect(f.ops.callsOf("clearTask")).To(HaveLen(1))
		sets := f.ops.callsOf("setTask")
		Expect(sets).To(HaveLen(1))
		Expect(sets[0].id).To(Equal(task.ID))
		Expect(sets[0].key).To(Equal("claude_session_started"))
		Expect(sets[0].value).To(Equal(f.params.Now.Now().UTC().Format(time.RFC3339Nano)))

		By("leaving the relaunch's registry record in flight, not finished")
		state, present := f.reg.State(f.vault.Name, task.ID)
		Expect(present).To(BeTrue())
		Expect(state).To(Equal(launchregistry.InFlight))
	})
})
