// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board_test

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/ops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/board"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	pageindexmocks "github.com/bborbe/vault-ui/pkg/pageindex/mocks"
)

var baseTime = time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)

type fakeVaults struct {
	vaults []board.Vault
	err    error
}

func (f fakeVaults) Vaults(_ context.Context) ([]board.Vault, error) {
	return f.vaults, f.err
}

type fakeList struct {
	items map[string][]ops.TaskListItem
	err   error
}

func (f fakeList) Execute(
	_ context.Context,
	_, _, pagesDir string,
	_ []string,
	_ bool,
	_, _ string,
) ([]ops.TaskListItem, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.items[pagesDir], nil
}

type fakeShow struct {
	result ops.EntityShowResult
	err    error
}

func (f fakeShow) Execute(
	_ context.Context,
	_, _, _ string,
) (ops.EntityShowResult, error) {
	return f.result, f.err
}

type fakeOps struct {
	list fakeList
	show fakeShow
}

func (f fakeOps) List(_ board.Vault) ops.ListOperation { return f.list }

func (f fakeOps) TopicShow(_ board.Vault) ops.EntityShowOperation { return f.show }

type fakeSignals struct {
	registry []string
	resume   []string
}

func (f fakeSignals) RegistrySessionIDs(_ context.Context) []string { return f.registry }

func (f fakeSignals) ResumeSessionIDs(_ context.Context) []string { return f.resume }

// fakeProbe is the injected session probe: it returns a fixed mtime and never
// touches the filesystem, so a spec can prove the board reads the probe.
type fakeProbe struct {
	mtime *libtime.DateTime
}

func (f fakeProbe) TranscriptMtime(_ context.Context, _, _, _ string) *libtime.DateTime {
	return f.mtime
}

type fakeCache struct {
	statuses map[string]string
	started  map[string]string
}

func (c fakeCache) LoadVault(_, _, _ string) error { return nil }

func (c fakeCache) GetStatus(_, itemID string) (string, bool) {
	status, ok := c.statuses[itemID]
	return status, ok
}

func (c fakeCache) GetSessionStarted(_, itemID string) (string, bool) {
	started, ok := c.started[itemID]
	return started, ok
}

func (c fakeCache) Count(_ string) int { return len(c.statuses) }

func (c fakeCache) Invalidate(_, _ string) {}

var vault = board.Vault{
	Name:         "personal",
	VaultName:    "Personal",
	Path:         "/vault",
	TasksFolder:  "24 Tasks",
	GoalsFolder:  "23 Goals",
	TopicsFolder: "23 Topics",
}

func item(name string, mutate ...func(*ops.TaskListItem)) ops.TaskListItem {
	entry := ops.TaskListItem{Name: name, Status: "todo"}
	for _, fn := range mutate {
		fn(&entry)
	}
	return entry
}

type harness struct {
	board    board.Board
	list     *fakeList
	show     *fakeShow
	cache    fakeCache
	signals  fakeSignals
	sessions fakeProbe
	launch   launchregistry.Registry
	index    *pageindexmocks.PageIndex
}

func newHarness(entries ...ops.TaskListItem) *harness {
	list := &fakeList{items: map[string][]ops.TaskListItem{"24 Tasks": entries}}
	show := &fakeShow{}
	cache := fakeCache{statuses: map[string]string{}, started: map[string]string{}}
	signals := fakeSignals{}
	launch := launchregistry.NewRegistry()
	index := &pageindexmocks.PageIndex{}
	h := &harness{
		list: list, show: show, cache: cache, signals: signals, launch: launch, index: index,
	}
	h.build()
	return h
}

// page builds one in-memory vault page with real markdown content, so the
// board's open-questions read runs storage.ParseOpenQuestions against it.
func page(name, content string) *domain.Page {
	return domain.NewPage(
		map[string]any{"status": "todo"},
		domain.FileMetadata{Name: name},
		domain.Content(content),
	)
}

// build (re)constructs the board from the current fakes, so a test can mutate a
// fake and rebuild.
func (h *harness) build() {
	h.board = board.New(board.Deps{
		Vaults:    fakeVaults{vaults: []board.Vault{vault}},
		Ops:       fakeOps{list: *h.list, show: *h.show},
		Cache:     h.cache,
		Launch:    h.launch,
		Clock:     libtime.CurrentDateTimeGetterFunc(func() libtime.DateTime { return libtime.DateTime(baseTime) }),
		Signals:   h.signals,
		Sessions:  h.sessions,
		PageIndex: h.index,
	})
}

var _ = Describe("ListVaults", func() {
	It("returns the configured vaults", func() {
		h := newHarness()
		responses, err := h.board.ListVaults(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(1))
		Expect(responses[0].Name).To(Equal("personal"))
		Expect(responses[0].VaultPath).To(Equal("/vault"))
		Expect(responses[0].TasksFolder).To(Equal("24 Tasks"))
		Expect(responses[0].ClaudeScript).To(Equal(""))
	})
})

var _ = Describe("ListTasks", func() {
	It("applies the default status set", func() {
		h := newHarness(
			item("Keep", func(i *ops.TaskListItem) { i.Status = "in_progress" }),
			item("Drop", func(i *ops.TaskListItem) { i.Status = "backlog" }),
		)
		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(1))
		Expect(responses[0].ID).To(Equal("Keep"))
	})

	It("filters by an explicit status list", func() {
		h := newHarness(
			item("Todo", func(i *ops.TaskListItem) { i.Status = "todo" }),
			item("Done", func(i *ops.TaskListItem) {
				i.Status = "completed"
				i.CompletedDate = baseTime.Add(-time.Hour).Format(time.RFC3339)
			}),
		)
		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{
			Statuses: []string{"completed"}, UpcomingHours: 8,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(1))
		Expect(responses[0].ID).To(Equal("Done"))
	})

	It("splits comma-separated and repeated status values, dropping empties", func() {
		h := newHarness(
			item("Todo", func(i *ops.TaskListItem) { i.Status = "todo" }),
			item("Next", func(i *ops.TaskListItem) { i.Status = "next" }),
			item("Hold", func(i *ops.TaskListItem) { i.Status = "hold" }),
		)
		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{
			Statuses: []string{"todo,next", "hold", " , "}, UpcomingHours: 8,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(3))
	})

	DescribeTable("filters by phase",
		func(phase string, expected []string) {
			h := newHarness(
				item("Exec", func(i *ops.TaskListItem) { i.Phase = "execution" }),
				item("Weird", func(i *ops.TaskListItem) { i.Phase = "bogus" }),
				item("None", func(i *ops.TaskListItem) { i.Phase = "" }),
			)
			responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{
				Phases: []string{phase}, UpcomingHours: 8,
			})
			Expect(err).NotTo(HaveOccurred())
			names := make([]string, 0, len(responses))
			for _, response := range responses {
				names = append(names, response.ID)
			}
			Expect(names).To(ConsistOf(expected))
		},
		Entry("valid phase", "execution", []string{"Exec"}),
		Entry("todo matches unknown phases", "todo", []string{"Weird", "None"}),
	)

	It("keeps an empty assignee token as the unassigned match", func() {
		h := newHarness(
			item("Assigned", func(i *ops.TaskListItem) { i.Assignee = "alice" }),
			item("Unassigned"),
		)
		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{
			Assignees: []string{""}, UpcomingHours: 8,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(1))
		Expect(responses[0].ID).To(Equal("Unassigned"))
	})

	It("filters by goal, stripping wikilink brackets", func() {
		h := newHarness(
			item("Tracked", func(i *ops.TaskListItem) { i.Goals = []string{"[[GoalOne]]"} }),
			item("Other", func(i *ops.TaskListItem) { i.Goals = []string{"[[GoalTwo]]"} }),
		)
		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{
			Goals: []string{"GoalOne"}, UpcomingHours: 8,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(1))
		Expect(responses[0].ID).To(Equal("Tracked"))
	})

	DescribeTable("applies the defer/upcoming window",
		func(deferDate string, hours int, visible bool, upcoming bool) {
			h := newHarness(item("Task", func(i *ops.TaskListItem) { i.DeferDate = deferDate }))
			responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{
				UpcomingHours: hours,
			})
			Expect(err).NotTo(HaveOccurred())
			if !visible {
				Expect(responses).To(BeEmpty())
				return
			}
			Expect(responses).To(HaveLen(1))
			Expect(responses[0].Upcoming).To(Equal(upcoming))
		},
		Entry("no defer date", "", 8, true, false),
		Entry("deferred in the past", "2020-01-01", 8, true, false),
		Entry("deferred far future", "2099-01-01", 8, false, false),
		Entry("deferred inside the window", "2026-10-05", 168, true, true),
	)

	It("marks a recently completed task done and forces phase done", func() {
		h := newHarness(item("Done", func(i *ops.TaskListItem) {
			i.Status = "completed"
			i.CompletedDate = baseTime.Add(-time.Hour).Format(time.RFC3339)
			i.Phase = "execution"
		}))
		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(1))
		Expect(responses[0].RecentlyCompleted).To(BeTrue())
		Expect(responses[0].Phase).NotTo(BeNil())
		Expect(*responses[0].Phase).To(Equal("done"))
	})

	It("drops a completed task older than the lookback window", func() {
		h := newHarness(item("Old", func(i *ops.TaskListItem) {
			i.Status = "completed"
			i.CompletedDate = baseTime.Add(-48 * time.Hour).Format(time.RFC3339)
		}))
		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(BeEmpty())
	})

	It("derives blocked and blockers from the status cache", func() {
		h := newHarness(
			item("Blocked", func(i *ops.TaskListItem) {
				i.BlockedBy = []string{"[[Open]]", "[[Closed]]"}
			}),
			item("Open"),
		)
		h.cache.statuses["Open"] = "in_progress"
		h.cache.statuses["Closed"] = "completed"
		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		var blocked boardTask
		for _, response := range responses {
			if response.ID == "Blocked" {
				blocked = boardTask{blocked: response.Blocked, blockers: response.Blockers}
			}
		}
		Expect(blocked.blocked).To(BeTrue())
		Expect(blocked.blockers).To(Equal([]string{"Open"}))
	})

	It("surfaces claude_session_started from the cache", func() {
		h := newHarness(item("Starting"))
		h.cache.started["Starting"] = "2026-10-04T11:00:00Z"
		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses[0].ClaudeSessionStarted).NotTo(BeNil())
		Expect(*responses[0].ClaudeSessionStarted).To(Equal("2026-10-04T11:00:00Z"))
	})

	It("suppresses the marker once the launch registry is finished", func() {
		h := newHarness(item("Starting"))
		h.cache.started["Starting"] = "2026-10-04T11:00:00Z"
		h.launch.Begin("personal", "Starting", "task")
		h.launch.Finish("personal", "Starting")
		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses[0].ClaudeSessionStarted).To(BeNil())
	})

	It("classifies a session from the registry", func() {
		h := newHarness(item("Live", func(i *ops.TaskListItem) {
			i.ClaudeSessionID = "11111111-1111-1111-1111-111111111111"
		}))
		h.signals.registry = []string{"11111111-1111-1111-1111-111111111111"}
		service := board.New(board.Deps{
			Vaults:    fakeVaults{vaults: []board.Vault{vault}},
			Ops:       fakeOps{list: *h.list, show: *h.show},
			Cache:     h.cache,
			Launch:    h.launch,
			Clock:     libtime.CurrentDateTimeGetterFunc(func() libtime.DateTime { return libtime.DateTime(baseTime) }),
			Signals:   h.signals,
			Sessions:  h.sessions,
			PageIndex: h.index,
		})
		responses, err := service.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses[0].SessionState).NotTo(BeNil())
		Expect(*responses[0].SessionState).To(Equal("live"))
	})

	It("classifies from the injected session probe, not the filesystem", func() {
		h := newHarness(item("Probed", func(i *ops.TaskListItem) {
			i.ClaudeSessionID = "22222222-2222-2222-2222-222222222222"
		}))
		// No transcript exists on disk anywhere; only the probe supplies one.
		h.sessions = fakeProbe{mtime: libtime.DateTime(baseTime).UTC().Ptr()}
		h.build()
		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(1))
		Expect(responses[0].SessionState).NotTo(BeNil())
		Expect(*responses[0].SessionState).To(Equal("live"))
		Expect(responses[0].ActivityDate).NotTo(BeNil())
	})

	It("reads a missing transcript from the injected probe, not the filesystem", func() {
		h := newHarness(item("Probed", func(i *ops.TaskListItem) {
			i.ClaudeSessionID = "22222222-2222-2222-2222-222222222222"
		}))
		h.sessions = fakeProbe{mtime: nil}
		h.build()
		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses[0].SessionState).NotTo(BeNil())
		Expect(*responses[0].SessionState).To(Equal("indeterminate"))
		Expect(responses[0].ActivityDate).To(BeNil())
	})

	It("filters to live sessions only", func() {
		h := newHarness(
			item("Live", func(i *ops.TaskListItem) {
				i.ClaudeSessionID = "11111111-1111-1111-1111-111111111111"
			}),
			item("Human"),
		)
		h.signals.registry = []string{"11111111-1111-1111-1111-111111111111"}
		h.build()
		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{
			UpcomingHours: 8, SessionLive: true,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(1))
		Expect(responses[0].ID).To(Equal("Live"))
	})

	It("builds the Obsidian URL with percent-encoded folder and file", func() {
		h := newHarness(item("Task With Space"))
		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses[0].ObsidianURL).To(Equal(
			"obsidian://open?vault=Personal&file=24%20Tasks/Task%20With%20Space.md",
		))
	})

	It("emits null for an absent priority and a number otherwise", func() {
		h := newHarness(
			item("NoPriority"),
			item("Priority", func(i *ops.TaskListItem) { i.Priority = 2 }),
		)
		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses[0].Priority).To(BeNil())
		Expect(responses[1].Priority).To(Equal(2))
	})

	It("skips an unknown vault instead of failing", func() {
		h := newHarness(item("Task"))
		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{
			Vaults: []string{"Nope"}, UpcomingHours: 8,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(BeEmpty())
	})

	It("propagates a list failure", func() {
		h := newHarness()
		h.list.err = errors.New("boom")
		h.build()
		_, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).To(HaveOccurred())
	})
})

type boardTask struct {
	blocked  bool
	blockers []string
}

var _ = Describe("ListGoals", func() {
	It("filters by status and assignee", func() {
		h := newHarness()
		h.list.items["23 Goals"] = []ops.TaskListItem{
			{Name: "GoalOne", Status: "in_progress", Assignee: "alice"},
			{Name: "GoalTwo", Status: "completed"},
		}
		responses, err := h.board.ListGoals(context.Background(), board.GoalQuery{
			Statuses: []string{"in_progress"}, Assignees: []string{"alice"}, UpcomingHours: 8,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(1))
		Expect(responses[0].ID).To(Equal("GoalOne"))
		Expect(responses[0].TargetDate).To(BeNil())
		Expect(responses[0].ObsidianURL).To(Equal(
			"obsidian://open?vault=Personal&file=23%20Goals/GoalOne.md",
		))
	})

	// An unrecognised goal status reaches the renderer as an empty string.
	// Python serialises it as "", so Go must too: a nil pointer here would
	// render null and diverge. The non-empty entry keeps the empty case from
	// being satisfied by ignoring the field entirely.
	DescribeTable("renders the status as a string, never null",
		func(status, expected string) {
			h := newHarness()
			h.list.items["23 Goals"] = []ops.TaskListItem{
				{Name: "Goal", Status: status},
			}
			responses, err := h.board.ListGoals(context.Background(), board.GoalQuery{UpcomingHours: 8})
			Expect(err).NotTo(HaveOccurred())
			Expect(responses).To(HaveLen(1))
			Expect(responses[0].Status).NotTo(BeNil())
			Expect(*responses[0].Status).To(Equal(expected))
		},
		Entry("unrecognised status renders as an empty string", "", ""),
		Entry("non-empty status passes through unchanged", "in_progress", "in_progress"),
	)

	It("keeps completed and undated goals visible and defers the rest", func() {
		h := newHarness()
		h.list.items["23 Goals"] = []ops.TaskListItem{
			{Name: "Done", Status: "completed"},
			{Name: "Undated", Status: "in_progress"},
			{Name: "Past", Status: "in_progress", DeferDate: "2020-01-01"},
			{Name: "Soon", Status: "in_progress", DeferDate: "2026-10-05"},
			{Name: "Far", Status: "in_progress", DeferDate: "2099-01-01"},
		}
		responses, err := h.board.ListGoals(context.Background(), board.GoalQuery{UpcomingHours: 168})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(4))
		upcoming := map[string]bool{}
		for _, response := range responses {
			upcoming[response.ID] = response.Upcoming
		}
		Expect(upcoming["Soon"]).To(BeTrue())
		Expect(upcoming["Past"]).To(BeFalse())
	})
})

var _ = Describe("ListTopics", func() {
	It("lists topics from the configured folder", func() {
		h := newHarness()
		h.list.items["23 Topics"] = []ops.TaskListItem{{Name: "TopicOne", Status: "in_progress"}}
		responses, err := h.board.ListTopics(context.Background(), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(1))
		Expect(responses[0].ObsidianURL).To(Equal(
			"obsidian://open?vault=Personal&file=23%20Topics/TopicOne.md",
		))
	})

	It("contributes nothing for a vault without a topics folder", func() {
		h := newHarness()
		noTopics := vault
		noTopics.TopicsFolder = ""
		service := board.New(board.Deps{
			Vaults:    fakeVaults{vaults: []board.Vault{noTopics}},
			Ops:       fakeOps{list: *h.list, show: *h.show},
			Cache:     h.cache,
			Launch:    h.launch,
			Clock:     libtime.CurrentDateTimeGetterFunc(func() libtime.DateTime { return libtime.DateTime(baseTime) }),
			Signals:   h.signals,
			Sessions:  h.sessions,
			PageIndex: h.index,
		})
		responses, err := service.ListTopics(context.Background(), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(BeEmpty())
	})
})

var _ = Describe("ShowTopic", func() {
	It("classifies the ## Goals entries against goals and tasks", func() {
		h := newHarness()
		h.show.result = ops.EntityShowResult{
			Name:    "TopicOne",
			Vault:   "personal",
			Fields:  map[string]string{"status": "in_progress"},
			Content: "# T\n\n## Goals\n- [[GoalOne]]\n- [[TaskOne]]\n- [[Missing]]\n\n## Notes\n- [[Ignored]]\n",
		}
		h.list.items["23 Goals"] = []ops.TaskListItem{{Name: "GoalOne", Status: "in_progress"}}
		h.list.items["24 Tasks"] = []ops.TaskListItem{{Name: "TaskOne", Status: "todo"}}
		h.build()
		response, err := h.board.ShowTopic(context.Background(), "personal", "TopicOne")
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Goals).To(Equal([]string{"GoalOne"}))
		Expect(response.Tasks).To(Equal([]string{"TaskOne"}))
		Expect(response.Unresolved).To(Equal([]string{"Missing"}))
		Expect(response.Status).To(Equal("in_progress"))
	})

	It("defaults the status to unknown", func() {
		h := newHarness()
		h.show.result = ops.EntityShowResult{Name: "TopicOne", Content: ""}
		h.build()
		response, err := h.board.ShowTopic(context.Background(), "personal", "TopicOne")
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal("unknown"))
	})

	It("rejects an unknown vault", func() {
		h := newHarness()
		_, err := h.board.ShowTopic(context.Background(), "Nope", "TopicOne")
		Expect(err).To(HaveOccurred())
		var unknown board.UnknownVaultError
		Expect(errors.As(err, &unknown)).To(BeTrue())
		Expect(unknown.Error()).To(Equal("Unknown vault: Nope"))
	})
})

var _ = Describe("date-time rendering", func() {
	// dateTimeString is reached from three call sites: TaskResponse.ModifiedDate
	// and TaskResponse.ActivityDate in tasks.go, and GoalResponse.ActivityDate in
	// goals.go. The table below exercises all three through the public entry
	// points, never by exporting or re-implementing the helper.
	DescribeTable("renders with Python's microsecond precision",
		func(modifiedDate, expected string) {
			h := newHarness(item("Task", func(i *ops.TaskListItem) { i.ModifiedDate = modifiedDate }))
			responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
			Expect(err).NotTo(HaveOccurred())
			Expect(responses).To(HaveLen(1))
			Expect(responses[0].ModifiedDate).NotTo(BeNil())
			Expect(*responses[0].ModifiedDate).To(Equal(expected))
			Expect(responses[0].ActivityDate).NotTo(BeNil())
			Expect(*responses[0].ActivityDate).To(Equal(expected))
		},
		Entry("whole second", "2026-10-04T12:00:00Z", "2026-10-04T12:00:00Z"),
		Entry("500 nanoseconds has a zero microsecond component",
			"2026-10-04T12:00:00.000000500Z", "2026-10-04T12:00:00Z"),
		Entry("three fractional digits", "2026-10-04T12:00:00.123Z",
			"2026-10-04T12:00:00.123000Z"),
		Entry("eight fractional digits", "2026-10-04T12:00:00.12345678Z",
			"2026-10-04T12:00:00.123456Z"),
		Entry("nine fractional digits", "2026-10-04T12:00:00.123456789Z",
			"2026-10-04T12:00:00.123456Z"),
	)

	It("renders a goal's ActivityDate the same way", func() {
		h := newHarness()
		h.list.items["23 Goals"] = []ops.TaskListItem{
			{Name: "Goal", Status: "in_progress", ModifiedDate: "2026-10-04T12:00:00.123456789Z"},
		}
		responses, err := h.board.ListGoals(context.Background(), board.GoalQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(1))
		Expect(responses[0].ActivityDate).NotTo(BeNil())
		Expect(*responses[0].ActivityDate).To(Equal("2026-10-04T12:00:00.123456Z"))
	})
})

var _ = Describe("ListAssignees", func() {
	It("collects distinct assignees and flags unassigned work", func() {
		h := newHarness(
			item("A", func(i *ops.TaskListItem) { i.Assignee = "bob" }),
			item("B", func(i *ops.TaskListItem) { i.Assignee = "Alice" }),
			item("C"),
			item("D", func(i *ops.TaskListItem) { i.Assignee = "bob" }),
		)
		response, err := h.board.ListAssignees(context.Background(), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Named).To(Equal([]string{"Alice", "bob"}))
		Expect(response.HasUnassigned).To(BeTrue())
	})
})

var _ = Describe("ListTasks open_questions", func() {
	// questions is a real Open Questions section, so the assertions below are
	// made against storage.ParseOpenQuestions rather than against a hand-built
	// slice the test itself typed.
	const questions = `# Task One

## Open Questions

- Which vault?
- Answered already → **personal**

## Notes

- not a question
`

	It("serialises [] for a task with no Open Questions section", func() {
		h := newHarness(item("TaskOne"))
		h.index.ListPagesReturns(
			[]*domain.Page{page("TaskOne", "# Task One\n\n## Notes\n\n- nothing\n")}, nil,
		)

		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(1))
		Expect(responses[0].OpenQuestions).NotTo(BeNil())
		Expect(responses[0].OpenQuestions).To(BeEmpty())

		encoded, err := json.Marshal(responses[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(ContainSubstring(`"open_questions":[]`))
	})

	It("maps the parsed section items in section order", func() {
		h := newHarness(item("TaskOne"))
		h.index.ListPagesReturns([]*domain.Page{page("TaskOne", questions)}, nil)

		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(1))
		// Marker and Line are dropped, and the answered item is included with
		// its question text — its answer is deliberately not exposed.
		Expect(responses[0].OpenQuestions).To(Equal([]domain.OpenQuestion{
			{Index: 1, Text: "Which vault?"},
			{Index: 2, Text: "Answered already"},
		}))

		encoded, err := json.Marshal(responses[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(ContainSubstring(
			`"open_questions":[{"index":1,"text":"Which vault?"},` +
				`{"index":2,"text":"Answered already"}]`,
		))
	})

	It("lists the vault's pages once, not once per task", func() {
		h := newHarness(item("One"), item("Two"), item("Three"))
		h.index.ListPagesReturns([]*domain.Page{page("One", questions)}, nil)

		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(3))
		Expect(h.index.ListPagesCallCount()).To(Equal(1))
	})

	It("carries an empty list rather than failing when the pages cannot be listed", func() {
		h := newHarness(item("TaskOne"))
		h.index.ListPagesReturns(nil, errors.New("boom"))

		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(responses).To(HaveLen(1))
		Expect(responses[0].OpenQuestions).To(BeEmpty())
	})
})
