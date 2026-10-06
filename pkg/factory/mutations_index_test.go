// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/queue"
)

// ac5Fixture is a full factory wiring over a temp vault whose page storage is a
// counting fake, with the index warmed. No watcher runs, so only a dirty mark
// can refresh a key.
type ac5Fixture struct {
	handler  http.Handler
	seams    *countingSeams
	vaultDir string
	tasksKey [2]string
	queue    queue.Queue
}

// newAC5Fixture builds the fixture and warms the page index.
func newAC5Fixture() *ac5Fixture {
	loader, configPath, vaultDir := apiFixture()
	writeFile(vaultDir, "24 Tasks/Task B.md", "---\nstatus: next\n---\n# Task B\n")
	seams := newCountingSeams()
	pageIndex := factory.CreatePageIndex(seams, seams, libtime.NewCurrentDateTime())
	writeQueue := startWriteQueue()
	handler := indexHandler(loader, configPath, pageIndex, writeQueue)
	Expect(
		factory.CreatePageIndexWarmup(loader, configPath, pageIndex)(context.Background()),
	).To(Succeed())
	return &ac5Fixture{
		handler:  handler,
		seams:    seams,
		vaultDir: vaultDir,
		tasksKey: [2]string{vaultDir, "24 Tasks"},
		queue:    writeQueue,
	}
}

// drain waits until the fixture vault has no pending or in-flight write, so a
// following read or call-count assertion sees the applied write.
func (f *ac5Fixture) drain() {
	EventuallyWithOffset(1, f.queue.Done("personal")).Should(BeClosed())
}

// tasksCalls returns the tasks-folder ListPages call count.
func (f *ac5Fixture) tasksCalls() int {
	return f.seams.listCounts()[f.tasksKey]
}

// request issues a request against the fixture handler and returns the recorder.
func (f *ac5Fixture) request(method, target, body string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, req)
	return recorder
}

// listTasks reads GET /api/tasks and decodes the task list.
func (f *ac5Fixture) listTasks() []api.TaskResponse {
	recorder := f.request(http.MethodGet, "/api/tasks?vault=personal", "")
	ExpectWithOffset(1, recorder.Code).To(Equal(http.StatusOK))
	var tasks []api.TaskResponse
	ExpectWithOffset(1, json.Unmarshal(recorder.Body.Bytes(), &tasks)).To(Succeed())
	return tasks
}

// findTask returns the task with the given id.
func findTask(tasks []api.TaskResponse, id string) api.TaskResponse {
	for _, task := range tasks {
		if task.ID == id {
			return task
		}
	}
	Fail("task not found: " + id)
	return api.TaskResponse{}
}

var _ = Describe("Page index write invalidation", func() {
	It("reflects a publishing write on the next read with exactly one rebuild", func() {
		f := newAC5Fixture()
		before := f.tasksCalls()

		recorder := f.request(
			http.MethodPatch,
			"/api/tasks/Task%20A/phase?vault=personal",
			`{"phase":"execution"}`,
		)
		Expect(recorder.Code).To(Equal(http.StatusAccepted))
		f.drain()
		// The write itself only marks; it must not rebuild.
		Expect(f.tasksCalls()).To(Equal(before))

		task := findTask(f.listTasks(), "Task A")
		Expect(task.Phase).NotTo(BeNil())
		Expect(*task.Phase).To(Equal("execution"))
		afterRead := f.tasksCalls()
		Expect(afterRead).To(Equal(before + 1))

		recorder = f.request(
			http.MethodPatch,
			"/api/tasks/Task%20A/phase?vault=personal",
			`{"phase":"done"}`,
		)
		Expect(recorder.Code).To(Equal(http.StatusAccepted))
		f.drain()
		beforeConcurrent := f.tasksCalls()

		bodies := make([]string, 2)
		var wg sync.WaitGroup
		for i := range bodies {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				bodies[i] = f.request(
					http.MethodGet, "/api/tasks?vault=personal", "",
				).Body.String()
			}(i)
		}
		wg.Wait()

		// Two concurrent reads of a dirty key share one rebuild.
		Expect(f.tasksCalls()).To(Equal(beforeConcurrent + 1))
		Expect(bodies[0]).To(ContainSubstring(`"phase":"done"`))
		Expect(bodies[1]).To(ContainSubstring(`"phase":"done"`))
	})

	It("reflects a non-publishing session write on the next read", func() {
		f := newAC5Fixture()
		before := f.tasksCalls()

		recorder := f.request(
			http.MethodPatch,
			"/api/tasks/Task%20A/session?vault=personal",
			`{"claude_session_id":"33333333-3333-3333-3333-333333333333"}`,
		)
		Expect(recorder.Code).To(Equal(http.StatusAccepted))
		f.drain()
		Expect(f.tasksCalls()).To(Equal(before))

		task := findTask(f.listTasks(), "Task A")
		Expect(task.ClaudeSessionID).NotTo(BeNil())
		Expect(*task.ClaudeSessionID).To(Equal("33333333-3333-3333-3333-333333333333"))
		Expect(f.tasksCalls()).To(Equal(before + 1))

		// A further session write on another task, then two concurrent reads.
		recorder = f.request(
			http.MethodPatch,
			"/api/tasks/Task%20B/session?vault=personal",
			`{"claude_session_id":"44444444-4444-4444-4444-444444444444"}`,
		)
		Expect(recorder.Code).To(Equal(http.StatusAccepted))
		f.drain()
		beforeConcurrent := f.tasksCalls()

		bodies := make([]string, 2)
		var wg sync.WaitGroup
		for i := range bodies {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				bodies[i] = f.request(
					http.MethodGet, "/api/tasks?vault=personal", "",
				).Body.String()
			}(i)
		}
		wg.Wait()

		Expect(f.tasksCalls()).To(Equal(beforeConcurrent + 1))
		Expect(bodies[0]).To(ContainSubstring("44444444-4444-4444-4444-444444444444"))
		Expect(bodies[1]).To(ContainSubstring("44444444-4444-4444-4444-444444444444"))
	})

	It("serves an external edit from memory until POST /api/cache/reload", func() {
		f := newAC5Fixture()
		Expect(findTask(f.listTasks(), "Task A").Priority).To(BeNil())

		// An external edit on disk that no watcher and no write will announce.
		writeFile(
			f.vaultDir,
			"24 Tasks/Task A.md",
			"---\nstatus: next\npriority: 3\n---\n# Task A\n",
		)

		Expect(findTask(f.listTasks(), "Task A").Priority).To(BeNil(),
			"the index must keep serving the old page from memory")

		recorder := f.request(http.MethodPost, "/api/cache/reload", "")
		Expect(recorder.Code).To(Equal(http.StatusOK))

		Expect(findTask(f.listTasks(), "Task A").Priority).To(BeEquivalentTo(3))
	})
})
