// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing/fstest"

	libtime "github.com/bborbe/time"
	vcmocks "github.com/bborbe/vault-cli/mocks"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/bborbe/vault-cli/pkg/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/handler"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/mutations"
	"github.com/bborbe/vault-ui/pkg/mutations/mocks"
	"github.com/bborbe/vault-ui/pkg/pageindex"
	"github.com/bborbe/vault-ui/pkg/queue"
	"github.com/bborbe/vault-ui/pkg/sessionlock"
	"github.com/bborbe/vault-ui/pkg/statuscache"
	"github.com/bborbe/vault-ui/pkg/vaultconfig"
	"github.com/bborbe/vault-ui/pkg/websocket"
	wsmocks "github.com/bborbe/vault-ui/pkg/websocket/mocks"
)

const (
	qwTaskID    = "TaskOne"
	qwGoalID    = "GoalOne"
	qwTasksDir  = "24 Tasks"
	qwGoalsDir  = "23 Goals"
	qwSessionID = "33333333-3333-3333-3333-333333333333"
	qwOtherID   = "11111111-1111-1111-1111-111111111111"
)

// writeRecord is one fake vault write the consumer applied.
type writeRecord struct {
	op        string
	vaultPath string
	item      string
	key       string
	value     string
}

// recordingCache records every Invalidate into the fixture's shared event log.
type recordingCache struct {
	statuscache.Cache
	f *queuedFixture
}

func (c *recordingCache) Invalidate(vaultName, itemID string) {
	c.f.appendEvent("invalidate:" + vaultName + ":" + itemID)
	c.Cache.Invalidate(vaultName, itemID)
}

// staticConfig is a ConfigProvider returning a fixed config.
type staticConfig struct{ cfg *vaultconfig.Config }

func (c staticConfig) Load(context.Context) (*vaultconfig.Config, error) { return c.cfg, nil }

// queuedFixture drives the real router over a fake vault-cli op set and a real
// per-vault write queue, with one shared, mutex-guarded event log.
type queuedFixture struct {
	router      http.Handler
	queue       queue.Queue
	manager     *wsmocks.WebsocketConnectionManager
	index       *mocks.IndexInvalidator
	cache       *recordingCache
	personalDir string
	workDir     string

	mu       sync.Mutex
	applied  []writeRecord
	events   []string
	blockKey string
	release  chan struct{}

	showErr       error
	showSessionID string
	showPhase     string
	setFailKey    string
	setFailErr    error
	setPanicKey   string
	goalFailKey   string
	goalFailErr   error
}

// newQueuedFixture builds the fixture, wires the service and returns the real
// router. Its write queue's Consume runs until the spec ends.
func newQueuedFixture() *queuedFixture {
	f := &queuedFixture{release: make(chan struct{})}
	f.personalDir = GinkgoT().TempDir()
	f.workDir = GinkgoT().TempDir()

	cfg := &vaultconfig.Config{
		Vaults: []vaultconfig.Vault{
			{
				Name: "personal", Path: f.personalDir,
				TasksFolder: qwTasksDir, GoalsFolder: qwGoalsDir,
			},
			{
				Name: "work", Path: f.workDir,
				TasksFolder: qwTasksDir, GoalsFolder: qwGoalsDir,
			},
		},
		CurrentUser: "tester",
	}

	f.manager = &wsmocks.WebsocketConnectionManager{}
	f.manager.BroadcastStub = func(payload []byte) {
		f.appendEvent("broadcast:" + string(payload))
	}

	f.index = &mocks.IndexInvalidator{}
	f.index.MarkDirtyStub = func(...pageindex.Key) { f.appendEvent("mark") }
	f.index.MarkFileDirtyStub = func(pageindex.Key, string) { f.appendEvent("mark") }

	f.cache = &recordingCache{Cache: statuscache.NewCache(), f: f}

	show := &vcmocks.ShowOperation{}
	show.ExecuteStub = func(_ context.Context, _, _, taskName string) (ops.TaskDetail, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.showErr != nil {
			return ops.TaskDetail{}, f.showErr
		}
		return ops.TaskDetail{
			Name: taskName, Phase: f.showPhase, ClaudeSessionID: f.showSessionID,
		}, nil
	}

	set := &vcmocks.FrontmatterSetOperation{}
	set.ExecuteStub = func(
		_ context.Context, vaultPath, item, key, value, _, _, _ string, _ bool,
	) error {
		f.mu.Lock()
		panicKey, failKey, failErr := f.setPanicKey, f.setFailKey, f.setFailErr
		f.mu.Unlock()
		if panicKey != "" && panicKey == key {
			panic("boom")
		}
		if failKey != "" && failKey == key {
			return failErr
		}
		return f.apply("FrontmatterSet", vaultPath, item, key, value)
	}

	clear := &vcmocks.FrontmatterClearOperation{}
	clear.ExecuteStub = func(_ context.Context, vaultPath, item, key string) error {
		return f.apply("FrontmatterClear", vaultPath, item, key, "")
	}

	goalSet := &vcmocks.EntitySetOperation{}
	goalSet.ExecuteStub = func(
		_ context.Context, vaultPath, item, key, value, _, _ string,
	) error {
		f.mu.Lock()
		failKey, failErr := f.goalFailKey, f.goalFailErr
		f.mu.Unlock()
		if failKey != "" && failKey == key {
			return failErr
		}
		return f.apply("GoalSet", vaultPath, item, key, value)
	}

	goalClear := &vcmocks.EntityClearOperation{}
	goalClear.ExecuteStub = func(_ context.Context, vaultPath, item, key string) error {
		return f.apply("GoalClear", vaultPath, item, key, "")
	}

	// Answer records each answer so a request carrying answers can be asserted
	// against the queued write; its index is folded into the recorded value so a
	// mistyped json tag cannot pass silently.
	answer := &vcmocks.TaskAnswerOperation{}
	answer.ExecuteStub = func(
		_ context.Context, vaultPath, taskName, _ string, answers []domain.OpenAnswer,
	) (ops.MutationResult, error) {
		for _, item := range answers {
			if err := f.apply(
				"Answer", vaultPath, taskName, "answer",
				strconv.Itoa(item.Index)+"="+item.Answer,
			); err != nil {
				return ops.MutationResult{}, err
			}
		}
		return ops.MutationResult{Success: true, Name: taskName}, nil
	}

	approve := &vcmocks.TaskApproveOperation{}
	approve.ExecuteStub = func(
		ctx context.Context,
		vaultPath, taskName, vaultName, approvedBy, assignee, currentUser string,
	) (ops.MutationResult, error) {
		if err := f.apply("Approve", vaultPath, taskName, "approve", approvedBy); err != nil {
			return ops.MutationResult{}, err
		}
		return ops.MutationResult{Success: true, Name: taskName}, nil
	}

	f.queue = queue.NewQueue()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer GinkgoRecover()
		defer close(done)
		_ = f.queue.Consume(ctx)
	}()
	DeferCleanup(func() {
		cancel()
		<-done
	})

	service := mutations.New(mutations.Deps{
		Config: staticConfig{cfg: cfg},
		Ops: func(vaultconfig.Vault) vaultui.OpSet {
			return vaultui.OpSet{
				List:             ops.NewListOperation(storage.NewPageStorage(nil)),
				Show:             show,
				FrontmatterSet:   set,
				FrontmatterClear: clear,
				Answer:           answer,
				Approve:          approve,
				GoalSet:          goalSet,
				GoalClear:        goalClear,
			}
		},
		Cache:     f.cache,
		Launch:    launchregistry.NewRegistry(),
		Locks:     sessionlock.NewRegistry(),
		Publisher: websocket.NewMutationPublisher(f.manager),
		Index:     f.index,
		Queue:     f.queue,
		Clock:     libtime.NewCurrentDateTime(),
		HomeDir:   GinkgoT().TempDir(),
	})

	readiness := vaultui.NewReadiness()
	readiness.SetReady()
	staticFS := fstest.MapFS{
		"index.html": {Data: []byte("<html>index</html>")},
		"app.js":     {Data: []byte("console.log('app')")},
		"style.css":  {Data: []byte("body{}")},
	}
	f.router = handler.CreateHTTPRouter(&fakeBoard{}, service, staticFS, readiness, f.manager)
	return f
}

// apply records one fake write. When its key is the blocked key it waits for
// the release before recording, so a blocked write is invisible in the log.
func (f *queuedFixture) apply(op, vaultPath, item, key, value string) error {
	f.mu.Lock()
	block := f.blockKey != "" && f.blockKey == key
	release := f.release
	f.mu.Unlock()
	if block {
		<-release
	}
	f.mu.Lock()
	f.applied = append(f.applied, writeRecord{
		op: op, vaultPath: vaultPath, item: item, key: key, value: value,
	})
	f.mu.Unlock()
	return nil
}

// blockKeySet blocks the next write carrying key.
func (f *queuedFixture) blockKeySet(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockKey = key
}

// releaseBlock unblocks every blocked write and lets later ones through.
func (f *queuedFixture) releaseBlock() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.blockKey == "" {
		return
	}
	f.blockKey = ""
	close(f.release)
}

func (f *queuedFixture) appendEvent(event string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
}

// records returns a copy of the applied-write log.
func (f *queuedFixture) records() []writeRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]writeRecord, len(f.applied))
	copy(out, f.applied)
	return out
}

// eventsSnapshot returns a copy of the shared event log.
func (f *queuedFixture) eventsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.events))
	copy(out, f.events)
	return out
}

// countApplied counts applied writes carrying key.
func (f *queuedFixture) countApplied(key string) int {
	count := 0
	for _, record := range f.records() {
		if record.key == key {
			count++
		}
	}
	return count
}

// appliedValuesIn returns the values of the writes to key in vaultPath, in
// application order.
func (f *queuedFixture) appliedValuesIn(vaultPath, key string) []string {
	values := make([]string, 0)
	for _, record := range f.records() {
		if record.vaultPath == vaultPath && record.key == key {
			values = append(values, record.value)
		}
	}
	return values
}

// countEvents counts the shared event log entries with the given prefix.
func (f *queuedFixture) countEvents(prefix string) int {
	count := 0
	for _, event := range f.eventsSnapshot() {
		if strings.HasPrefix(event, prefix) {
			count++
		}
	}
	return count
}

// broadcastJSON decodes the i-th broadcast frame.
func (f *queuedFixture) broadcastJSON(i int) map[string]any {
	var frame map[string]any
	ExpectWithOffset(1, json.Unmarshal(f.manager.BroadcastArgsForCall(i), &frame)).To(Succeed())
	return frame
}

// broadcastsOfType counts the broadcasts whose type field equals frameType.
func (f *queuedFixture) broadcastsOfType(frameType string) int {
	count := 0
	for i := 0; i < f.manager.BroadcastCallCount(); i++ {
		var frame map[string]any
		if json.Unmarshal(f.manager.BroadcastArgsForCall(i), &frame) == nil &&
			frame["type"] == frameType {
			count++
		}
	}
	return count
}

// do issues a request against the router.
func (f *queuedFixture) do(method, target, body string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, req)
	return recorder
}

// drain waits until the named vault has no pending or in-flight write.
func (f *queuedFixture) drain(vault string) {
	EventuallyWithOffset(1, f.queue.Done(vault)).Should(BeClosed())
}

// queuedRoute is one row of the AC1 table.
type queuedRoute struct {
	method string
	target string
	body   string
	block  string
	want   string
}

var _ = Describe("Queued frontmatter writes", func() {
	DescribeTable("AC1 — 202 before the vault write lands",
		func(route queuedRoute) {
			f := newQueuedFixture()
			f.blockKeySet(route.block)

			recorder := f.do(route.method, route.target, route.body)
			Expect(recorder.Code).To(Equal(http.StatusAccepted))
			// The write is still blocked, so nothing has been applied.
			Expect(f.countApplied(route.block)).To(Equal(0))
			Expect(recorder.Body.String()).To(MatchJSON(route.want))

			f.releaseBlock()
			Eventually(func() int { return f.countApplied(route.block) }).Should(Equal(1))
			f.drain("personal")
		},
		Entry("task phase", queuedRoute{
			method: http.MethodPatch,
			target: "/api/tasks/TaskOne/phase?vault=personal",
			body:   `{"phase":"execution"}`,
			block:  "phase",
			want:   `{"status":"success","task_id":"TaskOne","phase":"execution"}`,
		}),
		Entry("task status", queuedRoute{
			method: http.MethodPatch,
			target: "/api/tasks/TaskOne/status?vault=personal",
			body:   `{"status":"backlog"}`,
			block:  "status",
			want:   `{"status":"success","task_id":"TaskOne","new_status":"backlog"}`,
		}),
		Entry("task flag", queuedRoute{
			method: http.MethodPatch,
			target: "/api/tasks/TaskOne/flag?vault=personal",
			body:   `{}`,
			block:  "flag",
			want:   `{"status":"success","task_id":"TaskOne","flag":true}`,
		}),
		Entry("assign task", queuedRoute{
			method: http.MethodPatch,
			target: "/api/tasks/TaskOne/assign-to-me?vault=personal",
			block:  "assignee",
			want:   `{"status":"success","task_id":"TaskOne","assignee":"tester"}`,
		}),
		Entry("set task session", queuedRoute{
			method: http.MethodPatch,
			target: "/api/tasks/TaskOne/session?vault=personal",
			body:   `{"claude_session_id":"` + qwSessionID + `"}`,
			block:  "claude_session_id",
			want: `{"status":"success","task_id":"TaskOne","claude_session_id":"` +
				qwSessionID + `"}`,
		}),
		Entry("clear task session", queuedRoute{
			method: http.MethodDelete,
			target: "/api/tasks/TaskOne/session?vault=personal",
			block:  "claude_session_id",
			want:   `{"status":"success","task_id":"TaskOne"}`,
		}),
		Entry("goal status", queuedRoute{
			method: http.MethodPatch,
			target: "/api/goals/GoalOne/status?vault=personal",
			body:   `{"status":"hold"}`,
			block:  "status",
			want:   `{"status":"success","goal_id":"GoalOne","new_status":"hold"}`,
		}),
		Entry("assign goal", queuedRoute{
			method: http.MethodPatch,
			target: "/api/goals/GoalOne/assign-to-me?vault=personal",
			block:  "assignee",
			want:   `{"status":"success","goal_id":"GoalOne","assignee":"tester"}`,
		}),
		Entry("clear goal session", queuedRoute{
			method: http.MethodDelete,
			target: "/api/goals/GoalOne/session?vault=personal",
			block:  "claude_session_id",
			want:   `{"status":"success","goal_id":"GoalOne"}`,
		}),
	)

	It("AC2 — applies one vault's writes in order without blocking another vault", func() {
		f := newQueuedFixture()
		f.blockKeySet("phase")

		Expect(f.do(
			http.MethodPatch, "/api/tasks/TaskOne/phase?vault=personal",
			`{"phase":"execution"}`,
		).Code).To(Equal(http.StatusAccepted))
		Expect(f.do(
			http.MethodPatch, "/api/tasks/TaskOne/phase?vault=personal",
			`{"phase":"done"}`,
		).Code).To(Equal(http.StatusAccepted))

		// A write to a second vault is applied while the first vault is blocked.
		Expect(f.do(
			http.MethodPatch, "/api/tasks/TaskOne/flag?vault=work", `{}`,
		).Code).To(Equal(http.StatusAccepted))
		Eventually(func() []string {
			return f.appliedValuesIn(f.workDir, "flag")
		}).Should(Equal([]string{"true"}))
		Expect(f.countApplied("phase")).To(Equal(0))

		f.releaseBlock()
		f.drain("personal")
		f.drain("work")
		Expect(f.appliedValuesIn(f.personalDir, "phase")).
			To(Equal([]string{"execution", "done"}))
	})

	It("AC3 — publishes the frame and marks dirty only after the write", func() {
		f := newQueuedFixture()
		f.blockKeySet("phase")

		Expect(f.do(
			http.MethodPatch, "/api/tasks/TaskOne/phase?vault=personal",
			`{"phase":"execution"}`,
		).Code).To(Equal(http.StatusAccepted))

		Expect(f.manager.BroadcastCallCount()).To(Equal(0))
		Expect(f.index.MarkFileDirtyCallCount()).To(Equal(0))
		Expect(f.countEvents("invalidate:")).To(Equal(0))

		f.releaseBlock()
		f.drain("personal")

		Eventually(f.manager.BroadcastCallCount).Should(Equal(1))
		Expect(f.broadcastJSON(0)).To(Equal(map[string]any{
			"type":      "task_updated",
			"task_id":   "TaskOne",
			"item_kind": "task",
			"vault":     "personal",
		}))
		// A queued write marks one item's file, not the whole folder.
		Expect(f.index.MarkFileDirtyCallCount()).To(Equal(1))
		markKey, markName := f.index.MarkFileDirtyArgsForCall(0)
		Expect(markKey).To(Equal(pageindex.NewKey(f.personalDir, qwTasksDir)))
		Expect(markName).To(Equal("TaskOne"))
		Expect(f.index.MarkDirtyCallCount()).To(Equal(0))
		Expect(f.countEvents("invalidate:personal:TaskOne")).To(Equal(1))
		Expect(f.eventsSnapshot()).To(Equal([]string{
			"invalidate:personal:TaskOne",
			"mark",
			"broadcast:" + string(websocket.TaskUpdatedFrame("personal", "TaskOne")),
		}))
	})

	It("AC3 — publishes the goal frame only after the goal write", func() {
		f := newQueuedFixture()
		f.blockKeySet("status")

		Expect(f.do(
			http.MethodPatch, "/api/goals/GoalOne/status?vault=personal",
			`{"status":"hold"}`,
		).Code).To(Equal(http.StatusAccepted))

		Expect(f.manager.BroadcastCallCount()).To(Equal(0))
		Expect(f.index.MarkFileDirtyCallCount()).To(Equal(0))
		Expect(f.countEvents("invalidate:")).To(Equal(0))

		f.releaseBlock()
		f.drain("personal")

		Eventually(f.manager.BroadcastCallCount).Should(Equal(1))
		Expect(f.broadcastJSON(0)).To(Equal(map[string]any{
			"type":      "goal_updated",
			"goal_id":   "GoalOne",
			"item_kind": "goal",
			"vault":     "personal",
		}))
		// A queued goal write marks the goal's own file in the goals key.
		Expect(f.index.MarkFileDirtyCallCount()).To(Equal(1))
		markKey, markName := f.index.MarkFileDirtyArgsForCall(0)
		Expect(markKey).To(Equal(pageindex.NewKey(f.personalDir, qwGoalsDir)))
		Expect(markName).To(Equal("GoalOne"))
		Expect(f.index.MarkDirtyCallCount()).To(Equal(0))
		Expect(f.countEvents("invalidate:personal:GoalOne")).To(Equal(1))
	})

	It("AC4 — a failed task write reverts and says so", func() {
		f := newQueuedFixture()
		writeQueuedTaskFile(f.personalDir)
		f.setFailKey = "phase"
		f.setFailErr = errors.New("permission denied")

		Expect(f.do(
			http.MethodPatch, "/api/tasks/TaskOne/phase?vault=personal",
			`{"phase":"execution"}`,
		).Code).To(Equal(http.StatusAccepted))

		f.drain("personal")

		Eventually(f.manager.BroadcastCallCount).Should(Equal(1))
		Expect(f.broadcastJSON(0)).To(Equal(map[string]any{
			"type":      "write_failed",
			"task_id":   "TaskOne",
			"item_kind": "task",
			"vault":     "personal",
			"reason":    "permission denied",
		}))
		Consistently(func() int { return f.broadcastsOfType("task_updated") }).
			Should(Equal(0))
		Expect(f.index.MarkFileDirtyCallCount()).To(Equal(0))
		Expect(f.index.MarkDirtyCallCount()).To(Equal(0))
		Expect(f.countEvents("invalidate:")).To(Equal(0))

		items, err := ops.NewListOperation(storage.NewPageStorage(nil)).Execute(
			context.Background(), f.personalDir, "personal", qwTasksDir, nil, true, "", "",
		)
		Expect(err).NotTo(HaveOccurred())
		found := false
		for _, item := range items {
			if item.Name == qwTaskID {
				found = true
				Expect(item.Phase).To(Equal("planning"))
			}
		}
		Expect(found).To(BeTrue(), "the task must still be listed")
	})

	It("AC4 — a failed goal write reverts and says so", func() {
		f := newQueuedFixture()
		f.goalFailKey = "status"
		f.goalFailErr = errors.New("permission denied")

		Expect(f.do(
			http.MethodPatch, "/api/goals/GoalOne/status?vault=personal",
			`{"status":"hold"}`,
		).Code).To(Equal(http.StatusAccepted))

		f.drain("personal")

		Eventually(f.manager.BroadcastCallCount).Should(Equal(1))
		Expect(f.broadcastJSON(0)).To(Equal(map[string]any{
			"type":      "write_failed",
			"task_id":   "GoalOne",
			"item_kind": "goal",
			"vault":     "personal",
			"reason":    "permission denied",
		}))
		Consistently(func() int { return f.broadcastsOfType("goal_updated") }).
			Should(Equal(0))
		Expect(f.index.MarkFileDirtyCallCount()).To(Equal(0))
		Expect(f.index.MarkDirtyCallCount()).To(Equal(0))
		Expect(f.countEvents("invalidate:")).To(Equal(0))
	})

	It("survives a panicking write and keeps applying later writes", func() {
		f := newQueuedFixture()
		f.setPanicKey = "phase"

		Expect(f.do(
			http.MethodPatch, "/api/tasks/TaskOne/phase?vault=personal",
			`{"phase":"execution"}`,
		).Code).To(Equal(http.StatusAccepted))
		f.drain("personal")

		Eventually(f.manager.BroadcastCallCount).Should(Equal(1))
		Expect(f.broadcastJSON(0)).To(HaveKeyWithValue("type", "write_failed"))
		Expect(f.broadcastJSON(0)).To(HaveKeyWithValue("task_id", "TaskOne"))

		// The consumer survives, so the next write to the same vault lands.
		f.mu.Lock()
		f.setPanicKey = ""
		f.mu.Unlock()
		Expect(f.do(
			http.MethodPatch, "/api/tasks/TaskOne/phase?vault=personal",
			`{"phase":"execution"}`,
		).Code).To(Equal(http.StatusAccepted))
		f.drain("personal")
		Expect(f.countApplied("phase")).To(Equal(1))
		Eventually(f.manager.BroadcastCallCount).Should(Equal(2))
	})

	It("decodes and queues the answers a todo → planning move carries", func() {
		f := newQueuedFixture()
		f.showPhase = "todo"

		recorder := f.do(
			http.MethodPatch, "/api/tasks/TaskOne/phase?vault=personal",
			`{"phase":"planning","answers":[{"index":1,"answer":"yes"}]}`,
		)
		Expect(recorder.Code).To(Equal(http.StatusAccepted))
		Expect(recorder.Body.String()).To(MatchJSON(
			`{"status":"success","task_id":"TaskOne","phase":"planning"}`,
		))

		f.drain("personal")
		// The recorded value carries the decoded index and answer, so a mistyped
		// json tag (which would leave Answers nil) fails this assertion.
		Expect(f.appliedValuesIn(f.personalDir, "answer")).To(Equal([]string{"1=yes"}))
		// The approval is asserted too: without this, a regression that dropped
		// the set.Approve.Execute call would still pass on the answer alone.
		Expect(f.appliedValuesIn(f.personalDir, "approve")).To(Equal([]string{"operator"}))
	})

	DescribeTable("AC1 negative control — validation answers synchronously and enqueues nothing",
		func(setup func(*queuedFixture), method, target, body string, want int, detail string) {
			f := newQueuedFixture()
			if setup != nil {
				setup(f)
			}
			recorder := f.do(method, target, body)
			Expect(recorder.Code).To(Equal(want))
			if detail != "" {
				Expect(recorder.Body.String()).To(ContainSubstring(detail))
			}
			Expect(f.records()).To(BeEmpty())
			Expect(f.manager.BroadcastCallCount()).To(Equal(0))
		},
		Entry("missing vault", nil,
			http.MethodPatch, "/api/tasks/TaskOne/phase", `{"phase":"execution"}`,
			http.StatusUnprocessableEntity, ""),
		Entry("malformed JSON", nil,
			http.MethodPatch, "/api/tasks/TaskOne/phase?vault=personal", `{`,
			http.StatusUnprocessableEntity, ""),
		Entry("out-of-enum status", nil,
			http.MethodPatch, "/api/tasks/TaskOne/status?vault=personal", `{"status":"bogus"}`,
			http.StatusUnprocessableEntity, "literal_error"),
		Entry("dash-prefixed task id", nil,
			http.MethodPatch, "/api/tasks/-bad/status?vault=personal", `{"status":"backlog"}`,
			http.StatusBadRequest, "must not start with"),
		Entry("unknown vault", nil,
			http.MethodPatch, "/api/tasks/TaskOne/phase?vault=nope", `{"phase":"execution"}`,
			http.StatusBadRequest, "Unknown vault: nope"),
		Entry("aborted without reason", nil,
			http.MethodPatch, "/api/tasks/TaskOne/status?vault=personal", `{"status":"aborted"}`,
			http.StatusBadRequest, "reason is required"),
		Entry("assign-to-me of a missing task",
			func(f *queuedFixture) { f.showErr = errors.New("missing") },
			http.MethodPatch, "/api/tasks/TaskOne/assign-to-me?vault=personal", "",
			http.StatusNotFound, "Task not found: TaskOne"),
		Entry("session set over a different session",
			func(f *queuedFixture) { f.showSessionID = qwOtherID },
			http.MethodPatch, "/api/tasks/TaskOne/session?vault=personal",
			`{"claude_session_id":"`+qwSessionID+`"}`,
			http.StatusConflict, "already holds session"),
		Entry("answer with a non-positive index", nil,
			http.MethodPatch, "/api/tasks/TaskOne/phase?vault=personal",
			`{"phase":"planning","answers":[{"index":0,"answer":"yes"}]}`,
			http.StatusUnprocessableEntity, "question index 0 is not a positive position"),
		Entry("answer with a blank text", nil,
			http.MethodPatch, "/api/tasks/TaskOne/phase?vault=personal",
			`{"phase":"planning","answers":[{"index":1,"answer":"   "}]}`,
			http.StatusUnprocessableEntity, "the answer to question 1 is empty"),
		Entry("answer naming the same index twice", nil,
			http.MethodPatch, "/api/tasks/TaskOne/phase?vault=personal",
			`{"phase":"planning","answers":[{"index":1,"answer":"a"},{"index":1,"answer":"b"}]}`,
			http.StatusUnprocessableEntity, "question 1 is answered more than once"),
		Entry("answer containing a newline", nil,
			http.MethodPatch, "/api/tasks/TaskOne/phase?vault=personal",
			`{"phase":"planning","answers":[{"index":1,"answer":"a\nb"}]}`,
			http.StatusUnprocessableEntity, "contains a line break"),
		Entry("answer containing the question/answer delimiter", nil,
			http.MethodPatch, "/api/tasks/TaskOne/phase?vault=personal",
			`{"phase":"planning","answers":[{"index":1,"answer":"a → **b"}]}`,
			http.StatusUnprocessableEntity, "contains the question/answer delimiter"),
		Entry("answer containing markdown bold", nil,
			http.MethodPatch, "/api/tasks/TaskOne/phase?vault=personal",
			`{"phase":"planning","answers":[{"index":1,"answer":"a**b"}]}`,
			http.StatusUnprocessableEntity, "contains **"),
		Entry("answers for a task that is not at todo",
			func(f *queuedFixture) { f.showPhase = "planning" },
			http.MethodPatch, "/api/tasks/TaskOne/phase?vault=personal",
			`{"phase":"planning","answers":[{"index":1,"answer":"yes"}]}`,
			http.StatusBadRequest, "is at phase planning"),
		Entry("answers on a non-planning phase", nil,
			http.MethodPatch, "/api/tasks/TaskOne/phase?vault=personal",
			`{"phase":"execution","answers":[{"index":1,"answer":"yes"}]}`,
			http.StatusBadRequest, "discards them"),
	)
})

// writeQueuedTaskFile writes the on-disk task the AC4 read-back inspects.
func writeQueuedTaskFile(vaultDir string) {
	dir := filepath.Join(vaultDir, qwTasksDir)
	ExpectWithOffset(1, os.MkdirAll(dir, 0o750)).To(Succeed())
	ExpectWithOffset(1, os.WriteFile(
		filepath.Join(dir, qwTaskID+".md"),
		[]byte("---\nstatus: in_progress\nphase: planning\n---\n# Task One\n"),
		0o600,
	)).To(Succeed())
}
