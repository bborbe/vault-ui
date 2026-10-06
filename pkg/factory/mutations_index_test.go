// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	// Count only what the test's own requests cause, not the warm-up build.
	seams.reset()
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

// tasksCalls returns the tasks-folder ListFiles call count.
func (f *ac5Fixture) tasksCalls() int {
	return f.seams.listCounts()[f.tasksKey]
}

// tasksReads returns the per-filename ReadPage counts of the tasks folder.
func (f *ac5Fixture) tasksReads() map[string]int {
	return f.seams.readsForKey(f.tasksKey)
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

// listTaskBodies issues two concurrent GET /api/tasks and returns their bodies,
// so a test can prove two readers share one re-read.
func (f *ac5Fixture) listTaskBodies() []string {
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
	return bodies
}

// taskIDs returns the ids of every task in a list response.
func taskIDs(tasks []api.TaskResponse) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
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
	It("marks only the written file for a queued task phase change", func() {
		f := newAC5Fixture()
		beforeReads := f.seams.readCount()
		beforeLists := f.tasksCalls()

		recorder := f.request(
			http.MethodPatch,
			"/api/tasks/Task%20A/phase?vault=personal",
			`{"phase":"execution"}`,
		)
		Expect(recorder.Code).To(Equal(http.StatusAccepted))
		f.drain()
		// The write itself only marks; it must not read or list.
		Expect(f.seams.readCount()).To(Equal(beforeReads))
		Expect(f.tasksCalls()).To(Equal(beforeLists))

		task := findTask(f.listTasks(), "Task A")
		Expect(task.Phase).NotTo(BeNil())
		Expect(*task.Phase).To(Equal("execution"))
		// Evidence: exactly one read of the written file, and no listing at all.
		Expect(f.seams.readCount() - beforeReads).To(Equal(1))
		Expect(f.tasksReads()).To(Equal(map[string]int{"Task A.md": 1}))
		Expect(f.tasksCalls()).To(Equal(beforeLists))

		recorder = f.request(
			http.MethodPatch,
			"/api/tasks/Task%20A/phase?vault=personal",
			`{"phase":"done"}`,
		)
		Expect(recorder.Code).To(Equal(http.StatusAccepted))
		f.drain()
		beforeConcurrent := f.seams.readCount()

		bodies := f.listTaskBodies()

		// Two concurrent reads of a per-file-marked key share one re-read.
		Expect(f.seams.readCount() - beforeConcurrent).To(Equal(1))
		Expect(bodies[0]).To(ContainSubstring(`"phase":"done"`))
		Expect(bodies[1]).To(ContainSubstring(`"phase":"done"`))
	})

	It("marks only the written file for a queued session write", func() {
		f := newAC5Fixture()
		beforeReads := f.seams.readCount()
		beforeLists := f.tasksCalls()

		recorder := f.request(
			http.MethodPatch,
			"/api/tasks/Task%20A/session?vault=personal",
			`{"claude_session_id":"33333333-3333-3333-3333-333333333333"}`,
		)
		Expect(recorder.Code).To(Equal(http.StatusAccepted))
		f.drain()
		Expect(f.seams.readCount()).To(Equal(beforeReads))
		Expect(f.tasksCalls()).To(Equal(beforeLists))

		task := findTask(f.listTasks(), "Task A")
		Expect(task.ClaudeSessionID).NotTo(BeNil())
		Expect(*task.ClaudeSessionID).To(Equal("33333333-3333-3333-3333-333333333333"))
		Expect(f.seams.readCount() - beforeReads).To(Equal(1))
		Expect(f.tasksReads()).To(Equal(map[string]int{"Task A.md": 1}))
		Expect(f.tasksCalls()).To(Equal(beforeLists))

		// A further session write on another task, then two concurrent reads.
		recorder = f.request(
			http.MethodPatch,
			"/api/tasks/Task%20B/session?vault=personal",
			`{"claude_session_id":"44444444-4444-4444-4444-444444444444"}`,
		)
		Expect(recorder.Code).To(Equal(http.StatusAccepted))
		f.drain()
		beforeConcurrent := f.seams.readCount()

		bodies := f.listTaskBodies()

		Expect(f.seams.readCount() - beforeConcurrent).To(Equal(1))
		Expect(bodies[0]).To(ContainSubstring("44444444-4444-4444-4444-444444444444"))
		Expect(bodies[1]).To(ContainSubstring("44444444-4444-4444-4444-444444444444"))
	})

	It("keeps a folder-level mark for the synchronous execute-command site", func() {
		f := newAC5Fixture()
		beforeLists := f.tasksCalls()
		beforeReads := f.seams.readCount()

		recorder := f.request(
			http.MethodPost,
			"/api/tasks/Task%20A/execute-command?vault=personal",
			`{"command":"defer-task"}`,
		)
		Expect(recorder.Code).To(Equal(http.StatusOK))
		// The synchronous site marks folder-level; marking alone reads nothing.
		Expect(f.seams.readCount()).To(Equal(beforeReads))

		tasks := f.listTasks()
		// Evidence: exactly one stat-diff listing, and only the file whose
		// fingerprint changed is re-read.
		Expect(f.tasksCalls()).To(Equal(beforeLists + 1))
		Expect(f.tasksReads()).To(Equal(map[string]int{"Task A.md": 1}))

		// The read served the post-write value: the deferred task now carries a
		// defer date, so the default list no longer shows it while the untouched
		// task stays.
		Expect(taskIDs(tasks)).NotTo(ContainElement("Task A"))
		Expect(taskIDs(tasks)).To(ContainElement("Task B"))
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

	It("AC5(iii) re-reads every file exactly once after POST /api/cache/reload", func() {
		f := newAC5Fixture()
		before := f.seams.readCount()

		recorder := f.request(http.MethodPost, "/api/cache/reload", "")
		Expect(recorder.Code).To(Equal(http.StatusOK))

		// Reading every key re-reads every file, fingerprints notwithstanding.
		Expect(f.request(
			http.MethodGet, "/api/tasks?vault=personal", "",
		).Code).To(Equal(http.StatusOK))
		Expect(f.request(
			http.MethodGet, "/api/goals?vault=personal", "",
		).Code).To(Equal(http.StatusOK))

		Expect(f.seams.readCount() - before).To(Equal(countMarkdownFiles(f.vaultDir)))
	})

	It("AC5(iv) falls back to a folder-level mark for a loose id match", func() {
		f := newAC5Fixture()
		// A file whose name only vault-cli's case-insensitive substring lookup
		// connects to the ids below.
		writeFile(
			f.vaultDir, "24 Tasks/Page Probe Task.md",
			"---\nstatus: next\n---\n# Page Probe Task\n",
		)

		for _, id := range []string{"probe", "page%20probe%20task"} {
			beforeLists := f.tasksCalls()
			beforeReads := f.seams.readCount()

			recorder := f.request(
				http.MethodPatch,
				"/api/tasks/"+id+"/phase?vault=personal",
				`{"phase":"execution"}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusAccepted))
			f.drain()
			Expect(f.seams.readCount()).To(Equal(beforeReads))

			tasks := f.listTasks()
			// Evidence: the id never became a file path, so the whole folder is
			// stat-diffed and the file vault-cli actually wrote is picked up.
			Expect(f.tasksCalls()).To(Equal(beforeLists+1), "id %q", id)
			probe := findTask(tasks, "Page Probe Task")
			Expect(probe.Phase).NotTo(BeNil())
			Expect(*probe.Phase).To(Equal("execution"))
		}
	})
})

// countMarkdownFiles returns the number of .md files under root.
func countMarkdownFiles(root string) int {
	count := 0
	_ = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			count++
		}
		return nil
	})
	return count
}
