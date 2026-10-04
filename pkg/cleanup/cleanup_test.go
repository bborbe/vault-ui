// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cleanup_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	libtime "github.com/bborbe/time"

	"github.com/bborbe/vault-ui/pkg/cleanup"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/sessionlock"
	"github.com/bborbe/vault-ui/pkg/statuscache"
)

// opsCall records one VaultOps invocation.
type opsCall struct {
	op    string // listTasks, listGoals, showTask, setTask, clearTask, setGoal, clearGoal
	id    string
	key   string
	value string
}

// fakeOps is the hand-written VaultOps fake: it records every call and lets a
// test force a list/show error or a blocking (timeout) show.
type fakeOps struct {
	tasks []cleanup.Item
	goals []cleanup.Item
	calls []opsCall

	listTasksErr error
	listGoalsErr error
	showTaskErr  error
	setTaskErr   error
	clearTaskErr error
	setGoalErr   error
	clearGoalErr error

	showTaskResult cleanup.Item
	// showTaskBlock makes ShowTask wait for ctx to be done and then return its
	// error, exercising the re-read timeout.
	showTaskBlock bool
	// onClearTask runs while a clear is in flight, to simulate a concurrent
	// writer.
	onClearTask func()
	// onSetTask runs while a set is in flight.
	onSetTask func()
}

func newFakeOps(tasks, goals []cleanup.Item) *fakeOps {
	return &fakeOps{tasks: tasks, goals: goals}
}

func (f *fakeOps) record(call opsCall) {
	f.calls = append(f.calls, call)
}

func (f *fakeOps) ListTasks(_ context.Context) ([]cleanup.Item, error) {
	f.record(opsCall{op: "listTasks"})
	return f.tasks, f.listTasksErr
}

func (f *fakeOps) ListGoals(_ context.Context) ([]cleanup.Item, error) {
	f.record(opsCall{op: "listGoals"})
	return f.goals, f.listGoalsErr
}

func (f *fakeOps) ShowTask(ctx context.Context, itemID string) (cleanup.Item, error) {
	f.record(opsCall{op: "showTask", id: itemID})
	if f.showTaskBlock {
		<-ctx.Done()
		return cleanup.Item{}, ctx.Err()
	}
	return f.showTaskResult, f.showTaskErr
}

func (f *fakeOps) SetTaskField(_ context.Context, itemID, key, value string) error {
	if f.onSetTask != nil {
		f.onSetTask()
	}
	f.record(opsCall{op: "setTask", id: itemID, key: key, value: value})
	return f.setTaskErr
}

func (f *fakeOps) ClearTaskField(_ context.Context, itemID, key string) error {
	if f.onClearTask != nil {
		f.onClearTask()
	}
	f.record(opsCall{op: "clearTask", id: itemID, key: key})
	return f.clearTaskErr
}

func (f *fakeOps) SetGoalField(_ context.Context, itemID, key, value string) error {
	f.record(opsCall{op: "setGoal", id: itemID, key: key, value: value})
	return f.setGoalErr
}

func (f *fakeOps) ClearGoalField(_ context.Context, itemID, key string) error {
	f.record(opsCall{op: "clearGoal", id: itemID, key: key})
	return f.clearGoalErr
}

func (f *fakeOps) callsOf(op string) []opsCall {
	filtered := []opsCall{}
	for _, call := range f.calls {
		if call.op == op {
			filtered = append(filtered, call)
		}
	}
	return filtered
}

// fixedNow is the injected sweep instant: 2026-09-03T12:00:00Z.
var fixedNow = libtime.DateTime(time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC))

// markerOlderThanTTL is two hours before fixedNow.
const markerOlderThanTTL = "2026-09-03T10:00:00Z"

// markerYoungerThanTTL is five minutes before fixedNow.
const markerYoungerThanTTL = "2026-09-03T11:55:00Z"

// markerWithinOrphanGrace is thirty seconds before fixedNow.
const markerWithinOrphanGrace = "2026-09-03T11:59:30Z"

const testUUID = "11111111-1111-1111-1111-111111111111"
const testUUID2 = "22222222-2222-2222-2222-222222222222"

// newVault returns a one-vault config rooted at vaultPath.
func newVault(vaultPath string) cleanup.Vault {
	return cleanup.Vault{Name: "personal", Path: vaultPath, TasksFolder: "24 Tasks"}
}

// liveNamesFunc returns a LiveNames func serving a fixed map.
func liveNamesFunc(names map[string]string) func(context.Context) map[string]string {
	return func(context.Context) map[string]string { return names }
}

// noLiveNames is the empty live map.
func noLiveNames() func(context.Context) map[string]string {
	return liveNamesFunc(map[string]string{})
}

// writeTranscript creates <projectDir>/<sessionID>.jsonl under homeDir.
func writeTranscript(homeDir, vaultPath, sessionID string) string {
	projectDir := cleanup.DeriveClaudeProjectDir(homeDir, vaultPath, "")
	ExpectWithOffset(1, os.MkdirAll(projectDir, 0o750)).To(Succeed())
	path := filepath.Join(projectDir, sessionID+".jsonl")
	ExpectWithOffset(1, os.WriteFile(path, []byte("{}\n"), 0o600)).To(Succeed())
	return path
}

// writeTitledTranscript writes a transcript whose current custom title is title.
func writeTitledTranscript(homeDir, vaultPath, sessionID, title string) {
	projectDir := cleanup.DeriveClaudeProjectDir(homeDir, vaultPath, "")
	ExpectWithOffset(1, os.MkdirAll(projectDir, 0o750)).To(Succeed())
	path := filepath.Join(projectDir, sessionID+".jsonl")
	line := `{"type":"custom-title","customTitle":"` + title + `"}` + "\n"
	ExpectWithOffset(1, os.WriteFile(path, []byte(line), 0o600)).To(Succeed())
}

// seedMarker writes a task file carrying claude_session_started and loads it into
// the status cache (the CLI does not emit the marker, so the cache is the only
// source the sweep reads).
func seedMarker(
	cache statuscache.Cache,
	vaultName, vaultPath, tasksFolder, itemID, marker string,
) {
	dir := filepath.Join(vaultPath, tasksFolder)
	ExpectWithOffset(1, os.MkdirAll(dir, 0o750)).To(Succeed())
	body := "---\nstatus: in_progress\nclaude_session_started: \"" + marker + "\"\n---\n\nbody\n"
	ExpectWithOffset(1, os.WriteFile(filepath.Join(dir, itemID+".md"), []byte(body), 0o600)).To(Succeed())
	ExpectWithOffset(1, cache.LoadVault(vaultName, vaultPath, tasksFolder)).To(Succeed())
}

// sweepFixture bundles the injectable dependencies a test needs to assert on.
type sweepFixture struct {
	params cleanup.SweepParams
	ops    *fakeOps
	reg    launchregistry.Registry
	lock   sessionlock.Registry
	cache  statuscache.Cache
	home   string
	vault  cleanup.Vault
}

// newFixture builds a SweepParams with every dependency wired and the frozen
// durations applied. opsFor overrides the ops factory when non-nil.
func newFixture(
	tasks, goals []cleanup.Item,
	vaultPath string,
	opsFor cleanup.VaultOpsFactory,
) *sweepFixture {
	home := GinkgoT().TempDir()
	vault := newVault(vaultPath)
	ops := newFakeOps(tasks, goals)
	reg := launchregistry.NewRegistry()
	lock := sessionlock.NewRegistry()
	cache := statuscache.NewCache()

	factory := opsFor
	if factory == nil {
		factory = func(cleanup.Vault) cleanup.VaultOps { return ops }
	}

	params := cleanup.SweepParams{
		Vaults:             []cleanup.Vault{vault},
		OpsFor:             factory,
		HomeDir:            home,
		CurrentUser:        "alice",
		LaunchRegistry:     reg,
		SessionLock:        lock,
		StatusCache:        cache,
		LiveNames:          noLiveNames(),
		Now:                fixedNow,
		MarkerTTL:          cleanup.DefaultMarkerTTL,
		OrphanGrace:        cleanup.DefaultOrphanGrace,
		SetFieldTimeout:    cleanup.DefaultSetFieldTimeout,
		LockAcquireTimeout: cleanup.DefaultLockAcquireTimeout,
		CleanupInterval:    cleanup.DefaultCleanupInterval,
	}

	return &sweepFixture{params: params, ops: ops, reg: reg, lock: lock, cache: cache, home: home, vault: vault}
}

func (f *sweepFixture) sweep() cleanup.Sweep {
	return cleanup.NewSweep(f.params)
}

func runSweep(f *sweepFixture) int {
	cleared, err := f.sweep().Run(context.Background())
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return cleared
}

// expectNoMutations asserts the sweep issued no write of any kind.
func expectNoMutations(f *sweepFixture) {
	for _, op := range []string{"setTask", "clearTask", "setGoal", "clearGoal"} {
		ExpectWithOffset(1, f.ops.callsOf(op)).To(BeEmpty(), "unexpected %s call", op)
	}
}

var _ = Describe("CleanupSweep main task pass", func() {
	DescribeTable("session-id retention gate",
		func(scenario func()) { scenario() },
		Entry("clear-requires-registry-record-and-absent-transcript", func() {
			By("retaining a valid UUID with a launch record but a PRESENT transcript")
			vaultPath := GinkgoT().TempDir()
			task := cleanup.Item{
				ID: "task-1", Title: "T", Status: "in_progress",
				Assignee: "alice", ClaudeSessionID: testUUID,
			}
			f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
			f.reg.Begin(f.vault.Name, task.ID, "task")
			writeTranscript(f.home, vaultPath, testUUID)

			Expect(runSweep(f)).To(Equal(0))
			Expect(f.ops.callsOf("clearTask")).To(BeEmpty())

			By("clearing the same id with the transcript absent and the record present")
			f2 := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
			f2.reg.Begin(f2.vault.Name, task.ID, "task")

			Expect(runSweep(f2)).To(Equal(1))
			Expect(f2.ops.callsOf("clearTask")).To(HaveLen(1))
			Expect(f2.ops.callsOf("clearTask")[0].key).To(Equal("claude_session_id"))
		}),
		Entry("foreign-assignee-retained", func() {
			vaultPath := GinkgoT().TempDir()
			task := cleanup.Item{
				ID: "task-1", Title: "T", Status: "in_progress",
				Assignee: "bob", ClaudeSessionID: testUUID,
			}
			f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
			f.reg.Begin(f.vault.Name, task.ID, "task")

			Expect(runSweep(f)).To(Equal(0))
			Expect(f.ops.callsOf("clearTask")).To(BeEmpty())
		}),
	)

	It("retains a current-user id with a present transcript", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{ID: "t", Title: "T", Assignee: "alice", ClaudeSessionID: testUUID}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		writeTranscript(f.home, vaultPath, testUUID)

		Expect(runSweep(f)).To(Equal(0))
		Expect(f.ops.callsOf("clearTask")).To(BeEmpty())
	})

	It("retains a missing transcript with NO registry record", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{ID: "t", Title: "T", Assignee: "alice", ClaudeSessionID: testUUID}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)

		Expect(runSweep(f)).To(Equal(0))
		Expect(f.ops.callsOf("clearTask")).To(BeEmpty())
	})

	It("retains an unresolvable display name with zero clear calls", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{ID: "t", Title: "T", Assignee: "alice", ClaudeSessionID: "trading-alerts"}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)

		Expect(runSweep(f)).To(Equal(0))
		expectNoMutations(f)
	})

	It("repairs a resolvable display name with SetTaskField and never clears", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{ID: "t", Title: "T", Assignee: "alice", ClaudeSessionID: "my-title"}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		writeTitledTranscript(f.home, vaultPath, testUUID, "my-title")

		Expect(runSweep(f)).To(Equal(0))
		sets := f.ops.callsOf("setTask")
		Expect(sets).To(HaveLen(1))
		Expect(sets[0].key).To(Equal("claude_session_id"))
		Expect(sets[0].value).To(Equal(testUUID))
		Expect(f.ops.callsOf("clearTask")).To(BeEmpty())
	})

	It("skips a session id containing a path separator", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{ID: "t", Title: "T", Assignee: "alice", ClaudeSessionID: "a/b"}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)

		Expect(runSweep(f)).To(Equal(0))
		expectNoMutations(f)
	})

	It("clears the started marker in lockstep when it was set", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{
			ID: "t", Title: "T", Assignee: "alice",
			ClaudeSessionID: testUUID, ClaudeSessionStarted: markerOlderThanTTL,
		}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		f.reg.Begin(f.vault.Name, task.ID, "task")

		Expect(runSweep(f)).To(Equal(1))
		keys := []string{}
		for _, call := range f.ops.callsOf("clearTask") {
			keys = append(keys, call.key)
		}
		Expect(keys).To(ContainElements("claude_session_id", "claude_session_started"))
	})

	It("does not clear the started marker when the item carries none", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{ID: "t", Title: "T", Assignee: "alice", ClaudeSessionID: testUUID}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		f.reg.Begin(f.vault.Name, task.ID, "task")

		Expect(runSweep(f)).To(Equal(1))
		Expect(f.ops.callsOf("clearTask")).To(HaveLen(1))
		Expect(f.ops.callsOf("clearTask")[0].key).To(Equal("claude_session_id"))
	})
})

var _ = Describe("CleanupSweep run loop and vault isolation", func() {
	It("continues to the next vault when one vault's list fails", func() {
		vaultPath := GinkgoT().TempDir()
		otherPath := GinkgoT().TempDir()
		goodTask := cleanup.Item{ID: "t", Title: "T", Assignee: "alice", ClaudeSessionID: testUUID}
		good := newFakeOps([]cleanup.Item{goodTask}, nil)
		bad := newFakeOps(nil, nil)
		bad.listTasksErr = errors.New("boom")

		f := newFixture(nil, nil, vaultPath, nil)
		f.params.Vaults = []cleanup.Vault{
			{Name: "personal", Path: vaultPath, TasksFolder: "24 Tasks"},
			{Name: "other", Path: otherPath, TasksFolder: "24 Tasks"},
		}
		f.params.OpsFor = func(vault cleanup.Vault) cleanup.VaultOps {
			if vault.Name == "other" {
				return bad
			}
			return good
		}
		f.reg.Begin("personal", goodTask.ID, "task")

		cleared, err := f.sweep().Run(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(cleared).To(Equal(1))
		Expect(good.callsOf("clearTask")).To(HaveLen(1))
	})

	It("runs until the context is cancelled", func() {
		vaultPath := GinkgoT().TempDir()
		f := newFixture(nil, nil, vaultPath, nil)
		f.params.CleanupInterval = 5 * time.Millisecond

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		var loopErr error
		go func() {
			defer close(done)
			loopErr = f.sweep().RunLoop(ctx)
		}()
		time.Sleep(30 * time.Millisecond)
		cancel()
		Eventually(done, time.Second).Should(BeClosed())
		Expect(loopErr).ToNot(HaveOccurred())
		Expect(f.ops.callsOf("listTasks")).ToNot(BeEmpty())
	})
})

var _ = Describe("CleanupSweep goal pass", func() {
	It("clears an unresolvable goal display name", func() {
		vaultPath := GinkgoT().TempDir()
		goal := cleanup.Item{ID: "g", Title: "G", Assignee: "alice", ClaudeSessionID: "ai-knowledge-sharing"}
		f := newFixture(nil, []cleanup.Item{goal}, vaultPath, nil)

		Expect(runSweep(f)).To(Equal(1))
		clears := f.ops.callsOf("clearGoal")
		Expect(clears).To(HaveLen(2))
		Expect(clears[0].key).To(Equal("claude_session_id"))
		Expect(clears[1].key).To(Equal("claude_session_started"))
	})

	It("retains a foreign-assignee goal even with a registry record", func() {
		vaultPath := GinkgoT().TempDir()
		goal := cleanup.Item{ID: "g", Title: "G", Assignee: "bob", ClaudeSessionID: testUUID}
		f := newFixture(nil, []cleanup.Item{goal}, vaultPath, nil)
		f.reg.Begin(f.vault.Name, goal.ID, "goal")

		Expect(runSweep(f)).To(Equal(0))
		Expect(f.ops.callsOf("clearGoal")).To(BeEmpty())
	})

	It("clears a stale goal session and its marker in lockstep", func() {
		vaultPath := GinkgoT().TempDir()
		goal := cleanup.Item{ID: "g", Title: "G", Assignee: "alice", ClaudeSessionID: testUUID}
		f := newFixture(nil, []cleanup.Item{goal}, vaultPath, nil)
		f.reg.Begin(f.vault.Name, goal.ID, "goal")

		Expect(runSweep(f)).To(Equal(1))
		keys := []string{}
		for _, call := range f.ops.callsOf("clearGoal") {
			keys = append(keys, call.key)
		}
		Expect(keys).To(ContainElements("claude_session_id", "claude_session_started"))
	})

	It("does not abort the task pass when the goal list fails", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{ID: "t", Title: "T", Assignee: "alice", ClaudeSessionID: testUUID}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		f.reg.Begin(f.vault.Name, task.ID, "task")
		f.ops.listGoalsErr = os.ErrNotExist

		Expect(runSweep(f)).To(Equal(1))
	})

	It("survives a goal list error that mentions a missing directory", func() {
		vaultPath := GinkgoT().TempDir()
		f := newFixture(nil, nil, vaultPath, nil)
		f.ops.listGoalsErr = &os.PathError{Op: "open", Path: "goals", Err: os.ErrNotExist}

		Expect(runSweep(f)).To(Equal(0))
	})
})

var _ = Describe("CleanupSweep marker TTL pass", func() {
	It("clears a marker older than the TTL with no registry record", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{ID: "t", Title: "T"}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		seedMarker(f.cache, f.vault.Name, vaultPath, "24 Tasks", task.ID, markerOlderThanTTL)

		Expect(runSweep(f)).To(Equal(1))
		clears := f.ops.callsOf("clearTask")
		Expect(clears).To(HaveLen(1))
		Expect(clears[0].key).To(Equal("claude_session_started"))
	})

	It("leaves a marker younger than the TTL alone", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{ID: "t", Title: "T"}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		seedMarker(f.cache, f.vault.Name, vaultPath, "24 Tasks", task.ID, markerYoungerThanTTL)

		Expect(runSweep(f)).To(Equal(0))
		Expect(f.ops.callsOf("clearTask")).To(BeEmpty())
	})

	It("treats an unparseable legacy marker as expired", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{ID: "t", Title: "T"}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		seedMarker(f.cache, f.vault.Name, vaultPath, "24 Tasks", task.ID, "true")

		Expect(runSweep(f)).To(Equal(1))
	})

	It("never clears a marker while an IN_FLIGHT record exists", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{ID: "t", Title: "T"}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		seedMarker(f.cache, f.vault.Name, vaultPath, "24 Tasks", task.ID, markerOlderThanTTL)
		f.reg.Begin(f.vault.Name, task.ID, "task")

		Expect(runSweep(f)).To(Equal(0))
		Expect(f.ops.callsOf("clearTask")).To(BeEmpty())
	})

	It("skips an id-bearing task whose transcript is absent", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{ID: "t", Title: "T", ClaudeSessionID: testUUID}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		seedMarker(f.cache, f.vault.Name, vaultPath, "24 Tasks", task.ID, markerOlderThanTTL)

		Expect(runSweep(f)).To(Equal(0))
		Expect(f.ops.callsOf("clearTask")).To(BeEmpty())
	})

	It("clears a stale goal marker from the cache", func() {
		vaultPath := GinkgoT().TempDir()
		goal := cleanup.Item{ID: "g", Title: "G"}
		f := newFixture(nil, []cleanup.Item{goal}, vaultPath, nil)
		seedMarker(f.cache, f.vault.Name, vaultPath, "24 Tasks", goal.ID, markerOlderThanTTL)

		Expect(runSweep(f)).To(Equal(1))
		Expect(f.ops.callsOf("clearGoal")[0].key).To(Equal("claude_session_started"))
	})
})

var _ = Describe("CleanupSweep resurrected-marker re-clear", func() {
	It("re-clears a finished task's marker exactly once and evicts the record", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{ID: "t", Title: "T"}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		seedMarker(f.cache, f.vault.Name, vaultPath, "24 Tasks", task.ID, markerYoungerThanTTL)
		f.reg.Begin(f.vault.Name, task.ID, "task")
		f.reg.Finish(f.vault.Name, task.ID)

		Expect(runSweep(f)).To(Equal(1))
		Expect(f.ops.callsOf("clearTask")).To(HaveLen(1))
		_, present := f.reg.State(f.vault.Name, task.ID)
		Expect(present).To(BeFalse())

		By("not clearing again on the next pass")
		f.ops.calls = nil
		Expect(runSweep(f)).To(Equal(0))
		Expect(f.ops.callsOf("clearTask")).To(BeEmpty())
	})

	It("evicts a finished record whose marker is already gone", func() {
		vaultPath := GinkgoT().TempDir()
		f := newFixture(nil, nil, vaultPath, nil)
		f.reg.Begin(f.vault.Name, "t", "task")
		f.reg.Finish(f.vault.Name, "t")

		Expect(runSweep(f)).To(Equal(0))
		_, present := f.reg.State(f.vault.Name, "t")
		Expect(present).To(BeFalse())
	})

	It("keeps the record when the re-clear fails", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{ID: "t", Title: "T"}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		seedMarker(f.cache, f.vault.Name, vaultPath, "24 Tasks", task.ID, markerOlderThanTTL)
		f.reg.Begin(f.vault.Name, task.ID, "task")
		f.reg.Finish(f.vault.Name, task.ID)
		f.ops.clearTaskErr = os.ErrPermission

		Expect(runSweep(f)).To(Equal(0))
		state, present := f.reg.State(f.vault.Name, task.ID)
		Expect(present).To(BeTrue())
		Expect(state).To(Equal(launchregistry.Finished))
	})

	It("keeps a record re-begun while the clear is in flight", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{ID: "t", Title: "T"}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		seedMarker(f.cache, f.vault.Name, vaultPath, "24 Tasks", task.ID, markerOlderThanTTL)
		f.reg.Begin(f.vault.Name, task.ID, "task")
		f.reg.Finish(f.vault.Name, task.ID)
		f.ops.onClearTask = func() {
			f.ops.onClearTask = nil // re-begin only once
			f.reg.Begin(f.vault.Name, task.ID, "task")
		}

		Expect(runSweep(f)).To(Equal(1))
		state, present := f.reg.State(f.vault.Name, task.ID)
		Expect(present).To(BeTrue())
		Expect(state).To(Equal(launchregistry.InFlight))
	})

	It("re-clears a finished goal's marker and evicts the record", func() {
		vaultPath := GinkgoT().TempDir()
		goal := cleanup.Item{ID: "g", Title: "G"}
		f := newFixture(nil, []cleanup.Item{goal}, vaultPath, nil)
		seedMarker(f.cache, f.vault.Name, vaultPath, "24 Tasks", goal.ID, markerOlderThanTTL)
		f.reg.Begin(f.vault.Name, goal.ID, "goal")
		f.reg.Finish(f.vault.Name, goal.ID)

		Expect(runSweep(f)).To(Equal(1))
		Expect(f.ops.callsOf("clearGoal")).To(HaveLen(1))
		_, present := f.reg.State(f.vault.Name, goal.ID)
		Expect(present).To(BeFalse())
	})

	It("does not cross-clear a task record on the goal path", func() {
		vaultPath := GinkgoT().TempDir()
		task := cleanup.Item{ID: "t", Title: "T"}
		f := newFixture([]cleanup.Item{task}, nil, vaultPath, nil)
		seedMarker(f.cache, f.vault.Name, vaultPath, "24 Tasks", task.ID, markerOlderThanTTL)
		f.reg.Begin(f.vault.Name, task.ID, "task")
		f.reg.Finish(f.vault.Name, task.ID)

		Expect(runSweep(f)).To(Equal(1))
		Expect(f.ops.callsOf("clearGoal")).To(BeEmpty())
	})
})

var _ = Describe("DeriveClaudeProjectDir", func() {
	It("derives from vaultPath when session_project_dir is empty", func() {
		home := "/Users/me"
		Expect(cleanup.DeriveClaudeProjectDir(home, "/Users/me/vault", "")).
			To(Equal("/Users/me/.claude/projects/-Users-me-vault"))
	})

	It("encodes the session override", func() {
		home := "/Users/me"
		Expect(cleanup.DeriveClaudeProjectDir(home, "/Users/me/vault", "/Users/me/other")).
			To(Equal("/Users/me/.claude/projects/-Users-me-other"))
	})

	It("expands a tilde in the session dir", func() {
		home := "/Users/me"
		Expect(cleanup.DeriveClaudeProjectDir(home, "/Users/me/vault", "~/Documents/Obsidian/Personal")).
			To(Equal("/Users/me/.claude/projects/-Users-me-Documents-Obsidian-Personal"))
	})

	It("returns the projects root for an empty vault and session dir", func() {
		home := "/Users/me"
		Expect(cleanup.DeriveClaudeProjectDir(home, "", "")).
			To(Equal("/Users/me/.claude/projects"))
	})
})
