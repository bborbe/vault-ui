// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cleanup_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/cleanup"
)

// unboundTask is the canonical empty-id task the re-bind pass operates on.
func unboundTask() cleanup.Item {
	return cleanup.Item{ID: "t", Title: "My Title", Status: "in_progress", Assignee: "alice"}
}

// showTaskErrOps fails ShowTask for one id, so a single-task failure can be
// shown not to abort the rest of the pass.
type showTaskErrOps struct {
	fake  *fakeOps
	errID string
}

func (o *showTaskErrOps) ListTasks(ctx context.Context) ([]cleanup.Item, error) {
	return o.fake.ListTasks(ctx)
}

func (o *showTaskErrOps) ListGoals(ctx context.Context) ([]cleanup.Item, error) {
	return o.fake.ListGoals(ctx)
}

func (o *showTaskErrOps) ShowTask(ctx context.Context, itemID string) (cleanup.Item, error) {
	if itemID == o.errID {
		o.fake.record(opsCall{op: "showTask", id: itemID})
		return cleanup.Item{}, errors.New("boom")
	}
	return o.fake.ShowTask(ctx, itemID)
}

func (o *showTaskErrOps) SetTaskField(ctx context.Context, itemID, key, value string) error {
	return o.fake.SetTaskField(ctx, itemID, key, value)
}

func (o *showTaskErrOps) ClearTaskField(ctx context.Context, itemID, key string) error {
	return o.fake.ClearTaskField(ctx, itemID, key)
}

func (o *showTaskErrOps) SetGoalField(ctx context.Context, itemID, key, value string) error {
	return o.fake.SetGoalField(ctx, itemID, key, value)
}

func (o *showTaskErrOps) ClearGoalField(ctx context.Context, itemID, key string) error {
	return o.fake.ClearGoalField(ctx, itemID, key)
}

var _ = Describe("CleanupSweep empty-id re-bind", func() {
	It("writes the resolved uuid for a unique live title with a transcript", func() {
		vaultPath := GinkgoT().TempDir()
		f := newFixture([]cleanup.Item{unboundTask()}, nil, vaultPath, nil)
		f.params.LiveNames = liveNamesFunc(map[string]string{"My Title": testUUID})
		writeTranscript(f.home, vaultPath, testUUID)

		Expect(runSweep(f)).To(Equal(0))
		sets := f.ops.callsOf("setTask")
		Expect(sets).To(HaveLen(1))
		Expect(sets[0].key).To(Equal("claude_session_id"))
		Expect(sets[0].value).To(Equal(testUUID))
	})

	It("skips a live session whose transcript is not in this vault's project dir", func() {
		vaultPath := GinkgoT().TempDir()
		f := newFixture([]cleanup.Item{unboundTask()}, nil, vaultPath, nil)
		f.params.LiveNames = liveNamesFunc(map[string]string{"My Title": testUUID})

		Expect(runSweep(f)).To(Equal(0))
		Expect(f.ops.callsOf("setTask")).To(BeEmpty())
	})

	It("skips a task when no live session carries the title", func() {
		vaultPath := GinkgoT().TempDir()
		f := newFixture([]cleanup.Item{unboundTask()}, nil, vaultPath, nil)
		writeTranscript(f.home, vaultPath, testUUID)

		Expect(runSweep(f)).To(Equal(0))
		Expect(f.ops.callsOf("setTask")).To(BeEmpty())
	})

	It("skips an ambiguous live title (absent from the live map)", func() {
		vaultPath := GinkgoT().TempDir()
		f := newFixture([]cleanup.Item{unboundTask()}, nil, vaultPath, nil)
		writeTranscript(f.home, vaultPath, testUUID)

		Expect(runSweep(f)).To(Equal(0))
		Expect(f.ops.callsOf("setTask")).To(BeEmpty())
	})

	DescribeTable("skips an ineligible task",
		func(mutate func(item *cleanup.Item), prepare func(f *sweepFixture)) {
			vaultPath := GinkgoT().TempDir()
			item := unboundTask()
			mutate(&item)
			f := newFixture([]cleanup.Item{item}, nil, vaultPath, nil)
			f.params.LiveNames = liveNamesFunc(map[string]string{"My Title": testUUID})
			writeTranscript(f.home, vaultPath, testUUID)
			if prepare != nil {
				prepare(f)
			}

			Expect(runSweep(f)).To(Equal(0))
			Expect(f.ops.callsOf("setTask")).To(BeEmpty())
		},
		Entry("completed", func(item *cleanup.Item) { item.Status = "completed" }, nil),
		Entry("aborted", func(item *cleanup.Item) { item.Status = "aborted" }, nil),
		Entry("foreign-assignee", func(item *cleanup.Item) { item.Assignee = "bob" }, nil),
		Entry("no-title", func(item *cleanup.Item) { item.Title = "" }, nil),
		Entry("mid-launch", func(item *cleanup.Item) {}, func(f *sweepFixture) {
			f.reg.Begin(f.vault.Name, "t", "task")
		}),
	)

	It("skips a task whose launch registry holds a FINISHED record", func() {
		vaultPath := GinkgoT().TempDir()
		f := newFixture([]cleanup.Item{unboundTask()}, nil, vaultPath, nil)
		f.params.LiveNames = liveNamesFunc(map[string]string{"My Title": testUUID})
		writeTranscript(f.home, vaultPath, testUUID)
		f.reg.Begin(f.vault.Name, "t", "task")
		f.reg.Finish(f.vault.Name, "t")

		Expect(runSweep(f)).To(Equal(0))
		Expect(f.ops.callsOf("setTask")).To(BeEmpty())
	})

	It("abandons the write when the under-lock re-read now holds a session id", func() {
		vaultPath := GinkgoT().TempDir()
		f := newFixture([]cleanup.Item{unboundTask()}, nil, vaultPath, nil)
		f.params.LiveNames = liveNamesFunc(map[string]string{"My Title": testUUID})
		writeTranscript(f.home, vaultPath, testUUID)
		f.ops.showTaskResult = cleanup.Item{ID: "t", ClaudeSessionID: testUUID2}

		Expect(runSweep(f)).To(Equal(0))
		Expect(f.ops.callsOf("setTask")).To(BeEmpty())
	})

	It("abandons the write when the under-lock re-read is now foreign-assigned", func() {
		vaultPath := GinkgoT().TempDir()
		f := newFixture([]cleanup.Item{unboundTask()}, nil, vaultPath, nil)
		f.params.LiveNames = liveNamesFunc(map[string]string{"My Title": testUUID})
		writeTranscript(f.home, vaultPath, testUUID)
		f.ops.showTaskResult = cleanup.Item{ID: "t", Assignee: "bob"}

		Expect(runSweep(f)).To(Equal(0))
		Expect(f.ops.callsOf("setTask")).To(BeEmpty())
	})

	It("writes nothing when the under-lock re-read times out", func() {
		vaultPath := GinkgoT().TempDir()
		f := newFixture([]cleanup.Item{unboundTask()}, nil, vaultPath, nil)
		f.params.LiveNames = liveNamesFunc(map[string]string{"My Title": testUUID})
		f.params.SetFieldTimeout = 20 * time.Millisecond
		writeTranscript(f.home, vaultPath, testUUID)
		f.ops.showTaskBlock = true

		Expect(runSweep(f)).To(Equal(0))
		Expect(f.ops.callsOf("setTask")).To(BeEmpty())
	})

	It("skips the task when the lock acquisition times out and continues", func() {
		vaultPath := GinkgoT().TempDir()
		f := newFixture([]cleanup.Item{unboundTask()}, nil, vaultPath, nil)
		f.params.LiveNames = liveNamesFunc(map[string]string{"My Title": testUUID})
		f.params.LockAcquireTimeout = 20 * time.Millisecond
		writeTranscript(f.home, vaultPath, testUUID)

		release, err := f.lock.Lock(context.Background(), f.vault.Name, "t")
		Expect(err).ToNot(HaveOccurred())
		defer release()

		Expect(runSweep(f)).To(Equal(0))
		Expect(f.ops.callsOf("setTask")).To(BeEmpty())
	})

	It("performs the write while holding the per-task lock", func() {
		vaultPath := GinkgoT().TempDir()
		f := newFixture([]cleanup.Item{unboundTask()}, nil, vaultPath, nil)
		f.params.LiveNames = liveNamesFunc(map[string]string{"My Title": testUUID})
		writeTranscript(f.home, vaultPath, testUUID)

		lockWasFree := false
		f.ops.onSetTask = func() {
			acquireCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			release, err := f.lock.Lock(acquireCtx, f.vault.Name, "t")
			if err == nil {
				lockWasFree = true
				release()
			}
		}

		Expect(runSweep(f)).To(Equal(0))
		Expect(f.ops.callsOf("setTask")).To(HaveLen(1))
		Expect(lockWasFree).To(BeFalse())
	})

	It("does not clear when the re-bind write fails", func() {
		vaultPath := GinkgoT().TempDir()
		f := newFixture([]cleanup.Item{unboundTask()}, nil, vaultPath, nil)
		f.params.LiveNames = liveNamesFunc(map[string]string{"My Title": testUUID})
		writeTranscript(f.home, vaultPath, testUUID)
		f.ops.setTaskErr = errors.New("write failed")

		Expect(runSweep(f)).To(Equal(0))
		Expect(f.ops.callsOf("setTask")).To(HaveLen(1))
		Expect(f.ops.callsOf("clearTask")).To(BeEmpty())
	})

	It("does not abort the pass when one ShowTask fails", func() {
		vaultPath := GinkgoT().TempDir()
		first := unboundTask()
		first.ID = "t1"
		first.Title = "First"
		second := unboundTask()
		second.ID = "t2"
		second.Title = "Second"
		f := newFixture([]cleanup.Item{first, second}, nil, vaultPath, nil)
		f.params.LiveNames = liveNamesFunc(map[string]string{
			"First": testUUID, "Second": testUUID2,
		})
		writeTranscript(f.home, vaultPath, testUUID)
		writeTranscript(f.home, vaultPath, testUUID2)
		f.params.OpsFor = func(cleanup.Vault) cleanup.VaultOps {
			return &showTaskErrOps{fake: f.ops, errID: "t1"}
		}

		Expect(runSweep(f)).To(Equal(0))
		sets := f.ops.callsOf("setTask")
		Expect(sets).To(HaveLen(1))
		Expect(sets[0].id).To(Equal("t2"))
	})
})
