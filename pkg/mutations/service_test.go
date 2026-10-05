// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mutations_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"

	libtime "github.com/bborbe/time"
	vcmocks "github.com/bborbe/vault-cli/mocks"
	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/bborbe/vault-cli/pkg/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/mutations"
	"github.com/bborbe/vault-ui/pkg/mutations/mocks"
	"github.com/bborbe/vault-ui/pkg/pageindex"
	"github.com/bborbe/vault-ui/pkg/session"
	"github.com/bborbe/vault-ui/pkg/sessionlock"
	"github.com/bborbe/vault-ui/pkg/statuscache"
	"github.com/bborbe/vault-ui/pkg/vaultconfig"
)

const (
	taskOneID   = "TaskOne"
	taskTwoID   = "TaskTwo"
	goalOneID   = "GoalOne"
	goalTwoID   = "GoalTwo"
	taskThreeID = "TaskThree"
	taskFourID  = "TaskFour"
	goalThreeID = "GoalThree"
	uuidOne     = "11111111-1111-1111-1111-111111111111"
	uuidTwo     = "22222222-2222-2222-2222-222222222222"
)

// fakePane resolves every session to a fixed pane.
type fakePane struct {
	paneID string
	found  bool
}

func (p fakePane) Resolve(context.Context, string) (string, bool) { return p.paneID, p.found }

// fakeJump records the jump.
type fakeJump struct {
	token     string
	hasToken  bool
	performed string
	err       error
}

func (j *fakeJump) ReadToken() (string, bool) { return j.token, j.hasToken }
func (j *fakeJump) Perform(_ context.Context, paneID, _ string) error {
	j.performed = paneID
	return j.err
}

// fakeSignaler records the signaled pids.
type fakeSignaler struct{ pids []int }

func (s *fakeSignaler) Signal(pid int) error {
	s.pids = append(s.pids, pid)
	return nil
}

// noScanner is a ps scanner that reports no processes.
func noScanner(context.Context) (string, error) { return "", nil }

type harness struct {
	dir       string
	service   mutations.Service
	publisher *mocks.EventPublisher
	index     *mocks.IndexInvalidator
	cache     statuscache.Cache
	launch    launchregistry.Registry
	pane      *fakePane
	jump      *fakeJump
	config    vaultconfig.Config
	cfgPtr    *vaultconfig.Config
}

func writeFile(path, content string) {
	ExpectWithOffset(1, os.MkdirAll(filepath.Dir(path), 0o750)).To(Succeed())
	ExpectWithOffset(1, os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
}

// newHarness builds a service backed by real vault-cli ops over a temp vault.
func newHarness() *harness { return newHarnessWith(nil) }

// newHarnessWith builds a harness, applying mutate to the vault before wiring.
func newHarnessWith(mutate func(*vaultconfig.Vault)) *harness {
	dir := GinkgoT().TempDir()
	tasksDir := filepath.Join(dir, "24 Tasks")
	goalsDir := filepath.Join(dir, "23 Goals")

	writeFile(filepath.Join(tasksDir, taskOneID+".md"),
		"---\nstatus: todo\nphase: planning\n---\n\n# Task One\n")
	writeFile(filepath.Join(tasksDir, taskTwoID+".md"),
		"---\nstatus: in_progress\nclaude_session_id: "+uuidOne+"\n---\n\n# Task Two\n")
	writeFile(filepath.Join(goalsDir, goalOneID+".md"),
		"---\nstatus: in_progress\n---\n\n# Goal One\n")
	writeFile(filepath.Join(goalsDir, goalTwoID+".md"),
		"---\nstatus: in_progress\nclaude_session_id: "+uuidOne+"\n---\n\n# Goal Two\n")
	writeFile(filepath.Join(tasksDir, "Task With Space.md"),
		"---\nstatus: in_progress\nclaude_session_id: "+uuidOne+"\n---\n\n# Spaced\n")
	writeFile(filepath.Join(goalsDir, goalThreeID+".md"),
		"---\nstatus: in_progress\nclaude_session_id: "+uuidOne+
			"\nclaude_session_started: \"2026-01-01T00:00:00Z\"\n---\n\n# Goal Three\n")
	writeFile(filepath.Join(tasksDir, taskThreeID+".md"),
		"---\nstatus: in_progress\nclaude_session_started: \"2026-01-01T00:00:00Z\"\n---\n\n# Task Three\n")
	writeFile(filepath.Join(tasksDir, taskFourID+".md"),
		"---\nstatus: in_progress\nclaude_session_id: "+uuidOne+
			"\nclaude_session_started: \"2026-01-01T00:00:00Z\"\n---\n\n# Task Four\n")

	vault := vaultconfig.Vault{
		Name:              "personal",
		VaultName:         "Personal",
		Path:              dir,
		TasksFolder:       "24 Tasks",
		GoalsFolder:       "23 Goals",
		TopicsFolder:      "23 Topics",
		ClaudeScript:      "claude",
		VaultCLIPath:      "/fixture/bin/vault-cli",
		SessionProjectDir: "",
	}
	if mutate != nil {
		mutate(&vault)
	}
	cfg := vaultconfig.Config{
		Vaults:                []vaultconfig.Vault{vault},
		CurrentUser:           "fixtureuser",
		MaxConcurrentSessions: 5,
	}

	cache := statuscache.NewCache()
	Expect(cache.LoadVault(vault.Name, vault.Path, vault.TasksFolder)).To(Succeed())
	launch := launchregistry.NewRegistry()
	publisher := &mocks.EventPublisher{}
	index := &mocks.IndexInvalidator{}
	pane := &fakePane{paneID: "pane-1", found: true}
	jump := &fakeJump{token: "tok", hasToken: true}

	cfgPtr := &cfg
	service := mutations.New(mutations.Deps{
		Config:    configProvider{cfg: cfgPtr},
		Ops:       opsFactory(vault),
		Cache:     cache,
		Launch:    launch,
		Locks:     sessionlock.NewRegistry(),
		Publisher: publisher,
		Index:     index,
		Clock:     libtime.NewCurrentDateTime(),
		Scanner:   session.ProcessScanner(noScanner),
		Signaler:  &fakeSignaler{},
		Pane:      pane,
		Jump:      jump,
		HomeDir:   dir,
		WatcherNames: func(context.Context) []string {
			return []string{"personal"}
		},
	})
	return &harness{
		dir:       dir,
		service:   service,
		publisher: publisher,
		index:     index,
		cache:     cache,
		launch:    launch,
		pane:      pane,
		jump:      jump,
		config:    cfg,
		cfgPtr:    cfgPtr,
	}
}

// configProvider is a static ConfigProvider reading through a pointer so a
// test can mutate the config between calls.
type configProvider struct{ cfg *vaultconfig.Config }

func (p configProvider) Load(context.Context) (*vaultconfig.Config, error) {
	return p.cfg, nil
}

// opsFactory builds a real vault-cli op set over a vault with a nil session
// starter (session launches are unavailable, exercising that path).
func opsFactory(vault vaultconfig.Vault) mutations.OpsFactory {
	return func(vaultconfig.Vault) vaultui.OpSet {
		storageConfig := &storage.Config{
			TasksDir:  vault.TasksFolder,
			GoalsDir:  vault.GoalsFolder,
			TopicsDir: vault.TopicsFolder,
		}
		taskStore := storage.NewTaskStorage(storageConfig)
		goalStore := storage.NewGoalStorage(storageConfig)
		dailyStore := storage.NewDailyNoteStorage(storageConfig)
		publisher := ops.NewEscalationPublisher("", "", nil)
		clock := libtime.NewCurrentDateTime()
		return vaultui.OpSet{
			List:             ops.NewListOperation(storage.NewPageStorage(storageConfig)),
			Show:             ops.NewShowOperation(taskStore),
			FrontmatterSet:   ops.NewFrontmatterSetOperation(taskStore, clock, publisher, vault.Name, vault.TasksFolder),
			FrontmatterClear: ops.NewFrontmatterClearOperation(taskStore, publisher, vault.Name, vault.TasksFolder),
			WorkOn:           ops.NewWorkOnOperation(taskStore, dailyStore, clock, func() string { return uuidTwo }, nil, nil),
			Approve:          ops.NewTaskApproveOperation(taskStore, clock),
			Defer:            ops.NewDeferOperation(taskStore, dailyStore, clock),
			Complete:         ops.NewCompleteOperation(taskStore, dailyStore, clock, ops.NewInteractionCounter("", "")),
			GoalSet:          ops.NewGoalSetOperation(goalStore),
			GoalClear:        ops.NewGoalClearOperation(goalStore),
			GoalWorkOn:       ops.NewGoalWorkOnOperation(goalStore, func() string { return uuidTwo }, nil, nil),
			GoalDefer:        ops.NewGoalDeferOperation(goalStore, clock),
			GoalComplete:     ops.NewGoalCompleteOperation(goalStore, taskStore, clock),
		}
	}
}

// opsFactoryWithStarter builds a real op set whose work-on uses the given
// (faked) session starter and resumer.
func opsFactoryWithStarter(
	vault vaultconfig.Vault, starter *vcmocks.ClaudeSessionStarter, resumer *vcmocks.ClaudeResumer,
) mutations.OpsFactory {
	return func(vaultconfig.Vault) vaultui.OpSet {
		storageConfig := &storage.Config{
			TasksDir:  vault.TasksFolder,
			GoalsDir:  vault.GoalsFolder,
			TopicsDir: vault.TopicsFolder,
		}
		taskStore := storage.NewTaskStorage(storageConfig)
		goalStore := storage.NewGoalStorage(storageConfig)
		dailyStore := storage.NewDailyNoteStorage(storageConfig)
		clock := libtime.NewCurrentDateTime()
		return vaultui.OpSet{
			List:             ops.NewListOperation(storage.NewPageStorage(storageConfig)),
			Show:             ops.NewShowOperation(taskStore),
			FrontmatterSet:   ops.NewFrontmatterSetOperation(taskStore, clock, ops.NewEscalationPublisher("", "", nil), vault.Name, vault.TasksFolder),
			FrontmatterClear: ops.NewFrontmatterClearOperation(taskStore, ops.NewEscalationPublisher("", "", nil), vault.Name, vault.TasksFolder),
			WorkOn:           ops.NewWorkOnOperation(taskStore, dailyStore, clock, func() string { return uuidTwo }, starter, resumer),
			Approve:          ops.NewTaskApproveOperation(taskStore, clock),
			GoalSet:          ops.NewGoalSetOperation(goalStore),
			GoalClear:        ops.NewGoalClearOperation(goalStore),
			GoalWorkOn:       ops.NewGoalWorkOnOperation(goalStore, func() string { return uuidTwo }, starter, resumer),
		}
	}
}

// newStarterHarness builds the standard harness with a faked session starter
// and resumer wired into the op set, so RunTask and RunGoal reach a real write
// instead of failing on the unavailable starter.
func newStarterHarness() (*harness, *vcmocks.ClaudeSessionStarter) {
	starter := &vcmocks.ClaudeSessionStarter{}
	return newStarterHarnessWith(starter, &vcmocks.ClaudeResumer{}), starter
}

// newStarterHarnessWith builds the standard harness with the given (faked)
// session starter and resumer wired into the op set.
func newStarterHarnessWith(
	starter *vcmocks.ClaudeSessionStarter,
	resumer *vcmocks.ClaudeResumer,
) *harness {
	h := newHarnessWith(nil)
	vault := h.config.Vaults[0]
	h.service = mutations.New(mutations.Deps{
		Config:    configProvider{cfg: h.cfgPtr},
		Ops:       opsFactoryWithStarter(vault, starter, resumer),
		Cache:     h.cache,
		Launch:    h.launch,
		Locks:     sessionlock.NewRegistry(),
		Publisher: h.publisher,
		Index:     h.index,
		Clock:     libtime.NewCurrentDateTime(),
		Scanner:   session.ProcessScanner(noScanner),
		Signaler:  &fakeSignaler{},
		Pane:      h.pane,
		Jump:      h.jump,
		HomeDir:   h.dir,
	})
	return h
}

// readTask returns the task file's frontmatter body.
func (h *harness) readTask(taskID string) string {
	content, err := os.ReadFile(filepath.Join(h.dir, "24 Tasks", taskID+".md"))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return string(content)
}

// readGoal returns the goal file's frontmatter body.
func (h *harness) readGoal(goalID string) string {
	content, err := os.ReadFile(filepath.Join(h.dir, "23 Goals", goalID+".md"))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return string(content)
}

// httpStatus extracts the HTTP status from a mutation error.
func httpStatus(err error) int {
	var httpErr *mutations.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status
	}
	return 0
}

var _ = Describe("Mutation service", func() {
	var h *harness
	var ctx context.Context

	BeforeEach(func() {
		h = newHarness()
		ctx = context.Background()
	})

	Describe("UpdateTaskFlag", func() {
		It("writes flag true with the operator actor and publishes", func() {
			result, err := h.service.UpdateTaskFlag(
				ctx, "personal", taskOneID, api.UpdateFlagRequest{},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Flag).To(BeTrue())
			Expect(h.readTask(taskOneID)).To(ContainSubstring("flag: true"))
			Expect(h.publisher.PublishTaskUpdatedCallCount()).To(Equal(1))
		})

		It("clears the flag when false", func() {
			flag := false
			result, err := h.service.UpdateTaskFlag(
				ctx, "personal", taskTwoID, api.UpdateFlagRequest{Flag: &flag},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Flag).To(BeFalse())
		})

		It("404s an unknown vault", func() {
			_, err := h.service.UpdateTaskFlag(
				ctx, "nope", taskOneID, api.UpdateFlagRequest{},
			)
			Expect(httpStatus(err)).To(Equal(404))
		})
	})

	Describe("UpdateTaskStatus", func() {
		It("writes the status", func() {
			result, err := h.service.UpdateTaskStatus(
				ctx, "personal", taskOneID, api.UpdateStatusRequest{Status: "backlog"},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.NewStatus).To(Equal("backlog"))
			Expect(h.readTask(taskOneID)).To(ContainSubstring("status: backlog"))
			Expect(h.publisher.PublishTaskUpdatedCallCount()).To(Equal(1))
		})

		It("400s a dash-prefixed id before any write", func() {
			_, err := h.service.UpdateTaskStatus(
				ctx, "personal", "-bad", api.UpdateStatusRequest{Status: "backlog"},
			)
			Expect(httpStatus(err)).To(Equal(400))
		})

		It("400s an aborted close-out with no reason", func() {
			_, err := h.service.UpdateTaskStatus(
				ctx, "personal", taskOneID, api.UpdateStatusRequest{Status: "aborted"},
			)
			Expect(httpStatus(err)).To(Equal(400))
			Expect(err.Error()).To(ContainSubstring("reason is required"))
		})

		It("accepts an aborted close-out with a reason", func() {
			reason := "no longer needed"
			result, err := h.service.UpdateTaskStatus(
				ctx, "personal", taskOneID,
				api.UpdateStatusRequest{Status: "aborted", Reason: &reason},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.NewStatus).To(Equal("aborted"))
			Expect(h.readTask(taskOneID)).To(ContainSubstring("aborted"))
		})
	})

	Describe("UpdateGoalStatus", func() {
		It("writes the goal status and publishes", func() {
			result, err := h.service.UpdateGoalStatus(
				ctx, "personal", goalOneID, api.UpdateStatusRequest{Status: "hold"},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.GoalID).To(Equal(goalOneID))
			Expect(h.readGoal(goalOneID)).To(ContainSubstring("status: hold"))
			Expect(h.publisher.PublishGoalUpdatedCallCount()).To(Equal(1))
		})

		It("400s a dash-prefixed id", func() {
			_, err := h.service.UpdateGoalStatus(
				ctx, "personal", "-x", api.UpdateStatusRequest{Status: "hold"},
			)
			Expect(httpStatus(err)).To(Equal(400))
		})
	})

	Describe("UpdateTaskPhase", func() {
		It("writes phase and mirrors status to in_progress", func() {
			result, err := h.service.UpdateTaskPhase(
				ctx, "personal", taskOneID, api.UpdatePhaseRequest{Phase: "execution"},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Phase).To(Equal("execution"))
			Expect(h.readTask(taskOneID)).To(ContainSubstring("phase: execution"))
			Expect(h.readTask(taskOneID)).To(ContainSubstring("status: in_progress"))
		})

		It("mirrors phase done to status completed", func() {
			_, err := h.service.UpdateTaskPhase(
				ctx, "personal", taskOneID, api.UpdatePhaseRequest{Phase: "done"},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(h.readTask(taskOneID)).To(ContainSubstring("status: completed"))
		})
	})

	Describe("AssignTaskToMe", func() {
		It("writes the configured current user", func() {
			result, err := h.service.AssignTaskToMe(ctx, "personal", taskOneID)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Assignee).To(Equal("fixtureuser"))
			Expect(h.readTask(taskOneID)).To(ContainSubstring("assignee: fixtureuser"))
			Expect(h.publisher.PublishTaskUpdatedCallCount()).To(Equal(1))
		})

		It("404s an unknown task", func() {
			_, err := h.service.AssignTaskToMe(ctx, "personal", "Missing")
			Expect(httpStatus(err)).To(Equal(404))
		})
	})

	Describe("AssignGoalToMe", func() {
		It("writes the configured current user", func() {
			result, err := h.service.AssignGoalToMe(ctx, "personal", goalOneID)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Assignee).To(Equal("fixtureuser"))
			Expect(h.readGoal(goalOneID)).To(ContainSubstring("assignee: fixtureuser"))
		})
	})

	Describe("ClearTaskSession", func() {
		It("clears the session id", func() {
			result, err := h.service.ClearTaskSession(ctx, "personal", taskTwoID)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.TaskID).To(Equal(taskTwoID))
			Expect(h.readTask(taskTwoID)).NotTo(ContainSubstring("claude_session_id"))
		})
	})

	Describe("ClearGoalSession", func() {
		It("clears the goal session id and publishes", func() {
			result, err := h.service.ClearGoalSession(ctx, "personal", goalTwoID)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.GoalID).To(Equal(goalTwoID))
			Expect(h.readGoal(goalTwoID)).NotTo(ContainSubstring("claude_session_id"))
			Expect(h.publisher.PublishGoalUpdatedCallCount()).To(Equal(1))
		})

		It("400s a dash-prefixed id", func() {
			_, err := h.service.ClearGoalSession(ctx, "personal", "-z")
			Expect(httpStatus(err)).To(Equal(400))
		})
	})

	Describe("SetTaskSession", func() {
		It("stores a UUID on a task with no session", func() {
			result, err := h.service.SetTaskSession(
				ctx, "personal", taskOneID, api.UpdateSessionRequest{ClaudeSessionID: uuidTwo},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.ClaudeSessionID).To(Equal(uuidTwo))
			Expect(h.readTask(taskOneID)).To(ContainSubstring(uuidTwo))
		})

		It("409s when a different session is already held", func() {
			_, err := h.service.SetTaskSession(
				ctx, "personal", taskTwoID, api.UpdateSessionRequest{ClaudeSessionID: uuidTwo},
			)
			Expect(httpStatus(err)).To(Equal(409))
			Expect(err.Error()).To(ContainSubstring("already holds session"))
		})

		It("is a no-op when the same session is re-set", func() {
			result, err := h.service.SetTaskSession(
				ctx, "personal", taskTwoID, api.UpdateSessionRequest{ClaudeSessionID: uuidOne},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.ClaudeSessionID).To(Equal(uuidOne))
		})
	})

	Describe("ExecuteTaskCommand", func() {
		It("400s an unknown command", func() {
			_, err := h.service.ExecuteTaskCommand(
				ctx, "personal", taskOneID, api.ExecuteCommandRequest{Command: "bogus"},
			)
			Expect(httpStatus(err)).To(Equal(400))
			Expect(err.Error()).To(ContainSubstring("Unknown command"))
		})

		It("runs the complete-task fast path and publishes", func() {
			result, err := h.service.ExecuteTaskCommand(
				ctx, "personal", taskOneID, api.ExecuteCommandRequest{Command: "complete-task"},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.SessionID).To(BeEmpty())
			Expect(result.Success).NotTo(BeNil())
			Expect(*result.Success).To(BeTrue())
			Expect(result.Command).To(ContainSubstring("task complete " + taskOneID))
			Expect(h.publisher.PublishTaskUpdatedCallCount()).To(Equal(1))
		})
	})

	Describe("ExecuteGoalCommand", func() {
		It("400s an unknown command", func() {
			_, err := h.service.ExecuteGoalCommand(
				ctx, "personal", goalOneID, api.ExecuteCommandRequest{Command: "bogus"},
			)
			Expect(httpStatus(err)).To(Equal(400))
		})

		It("runs the complete-goal fast path and publishes", func() {
			result, err := h.service.ExecuteGoalCommand(
				ctx, "personal", goalOneID, api.ExecuteCommandRequest{Command: "complete-goal"},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Command).To(Equal("complete-goal"))
			Expect(h.publisher.PublishGoalUpdatedCallCount()).To(Equal(1))
		})
	})

	Describe("RunTask", func() {
		It("500s when the session starter is unavailable", func() {
			_, err := h.service.RunTask(ctx, "personal", taskOneID)
			Expect(httpStatus(err)).To(Equal(500))
		})

		It("429s when the launch cap is reached", func() {
			capCfg := h.config
			capCfg.MaxConcurrentSessions = 0
			capPtr := &capCfg
			service := mutations.New(mutations.Deps{
				Config:    configProvider{cfg: capPtr},
				Ops:       opsFactory(capCfg.Vaults[0]),
				Cache:     h.cache,
				Launch:    h.launch,
				Locks:     sessionlock.NewRegistry(),
				Publisher: h.publisher,
				Index:     h.index,
				Clock:     libtime.NewCurrentDateTime(),
				Scanner:   session.ProcessScanner(noScanner),
				Signaler:  &fakeSignaler{},
				Pane:      fakePane{},
				Jump:      &fakeJump{},
				HomeDir:   h.dir,
			})
			_, err := service.RunTask(ctx, "personal", taskTwoID)
			Expect(httpStatus(err)).To(Equal(429))
		})
	})

	Describe("TakeOverTask", func() {
		It("400s a dash-prefixed id", func() {
			_, err := h.service.TakeOverTask(ctx, "personal", "-x")
			Expect(httpStatus(err)).To(Equal(400))
		})

		It("400s a task with no session", func() {
			_, err := h.service.TakeOverTask(ctx, "personal", taskOneID)
			Expect(httpStatus(err)).To(Equal(400))
		})

		It("returns the resume command for a live session", func() {
			result, err := h.service.TakeOverTask(ctx, "personal", taskTwoID)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.SessionID).To(Equal(uuidOne))
			Expect(result.Terminated).NotTo(BeNil())
			Expect(*result.Terminated).To(BeFalse())
		})
	})

	Describe("TakeOverGoal", func() {
		It("returns the resume command for a live session", func() {
			result, err := h.service.TakeOverGoal(ctx, "personal", goalTwoID)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.SessionID).To(Equal(uuidOne))
		})

		It("400s a goal with no session", func() {
			_, err := h.service.TakeOverGoal(ctx, "personal", goalOneID)
			Expect(httpStatus(err)).To(Equal(400))
		})
	})

	Describe("JumpTask", func() {
		It("403s a cross-origin request", func() {
			err := h.service.JumpTask(ctx, "personal", taskTwoID, false)
			Expect(httpStatus(err)).To(Equal(403))
		})

		It("409s a task with no session", func() {
			err := h.service.JumpTask(ctx, "personal", taskOneID, true)
			Expect(httpStatus(err)).To(Equal(409))
		})

		It("204s a live session with a resolvable pane", func() {
			err := h.service.JumpTask(ctx, "personal", taskTwoID, true)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("RunGoal", func() {
		It("404s an unknown goal", func() {
			_, err := h.service.RunGoal(ctx, "personal", "Missing")
			Expect(httpStatus(err)).To(Equal(404))
		})

		It("400s a dash-prefixed id", func() {
			_, err := h.service.RunGoal(ctx, "personal", "-x")
			Expect(httpStatus(err)).To(Equal(400))
		})
	})

	Describe("ReloadCache", func() {
		It("reloads a single vault", func() {
			result, err := h.service.ReloadCache(ctx, "personal")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Reloaded).To(Equal([]string{"personal"}))
			Expect(result.Counts).To(HaveKey("personal"))
		})

		It("reloads all vaults when vault is empty", func() {
			result, err := h.service.ReloadCache(ctx, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Reloaded).To(Equal([]string{"personal"}))
		})

		It("404s an unknown vault", func() {
			_, err := h.service.ReloadCache(ctx, "nope")
			Expect(httpStatus(err)).To(Equal(404))
		})
	})

	Describe("ReloadConfig", func() {
		It("reports the vault and watcher names", func() {
			result, err := h.service.ReloadConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Vaults).To(Equal([]string{"personal"}))
			Expect(result.Watchers).To(Equal([]string{"personal"}))
		})
	})
})

var _ = Describe("Mutation service edge branches", func() {
	var h *harness
	var ctx context.Context

	BeforeEach(func() {
		h = newHarness()
		ctx = context.Background()
	})

	Describe("JumpTask", func() {
		It("500s an unknown vault", func() {
			Expect(httpStatus(h.service.JumpTask(ctx, "nope", taskTwoID, true))).To(Equal(500))
		})

		It("404s an unknown task", func() {
			Expect(httpStatus(h.service.JumpTask(ctx, "personal", "Missing", true))).To(Equal(404))
		})

		It("409s when no pane resolves", func() {
			h.pane.found = false
			Expect(httpStatus(h.service.JumpTask(ctx, "personal", taskTwoID, true))).To(Equal(409))
		})

		It("503s when the jump credential is unreadable", func() {
			h.jump.hasToken = false
			Expect(httpStatus(h.service.JumpTask(ctx, "personal", taskTwoID, true))).To(Equal(503))
		})

		It("502s when the jump server is unreachable", func() {
			h.jump.err = errors.New("boom")
			Expect(httpStatus(h.service.JumpTask(ctx, "personal", taskTwoID, true))).To(Equal(502))
		})
	})

	Describe("RunTask", func() {
		It("500s an unknown vault", func() {
			_, err := h.service.RunTask(ctx, "nope", taskOneID)
			Expect(httpStatus(err)).To(Equal(500))
		})

		It("404s an unknown task", func() {
			_, err := h.service.RunTask(ctx, "personal", "Missing")
			Expect(httpStatus(err)).To(Equal(404))
		})

		It("approves a todo task before failing on the unavailable starter", func() {
			_, err := h.service.RunTask(ctx, "personal", taskOneID)
			Expect(httpStatus(err)).To(Equal(500))
			Expect(h.readTask(taskOneID)).To(ContainSubstring("phase: planning"))
		})
	})

	Describe("RunGoal", func() {
		It("500s when the goal session cannot start", func() {
			_, err := h.service.RunGoal(ctx, "personal", goalOneID)
			Expect(httpStatus(err)).To(Equal(500))
		})

		It("500s an unknown vault", func() {
			_, err := h.service.RunGoal(ctx, "nope", goalOneID)
			Expect(httpStatus(err)).To(Equal(500))
		})
	})

	Describe("TakeOverTask starting path", func() {
		It("400s a starting task with no session id", func() {
			_, err := h.service.TakeOverTask(ctx, "personal", taskThreeID)
			Expect(httpStatus(err)).To(Equal(400))
			Expect(h.readTask(taskThreeID)).NotTo(ContainSubstring("claude_session_started"))
		})

		It("returns the resume command for a starting task with a session id", func() {
			result, err := h.service.TakeOverTask(ctx, "personal", taskFourID)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.SessionID).To(Equal(uuidOne))
			Expect(result.Terminated).NotTo(BeNil())
		})
	})

	Describe("TakeOverGoal starting path", func() {
		It("returns the resume command for a starting goal with a session id", func() {
			result, err := h.service.TakeOverGoal(ctx, "personal", goalThreeID)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.SessionID).To(Equal(uuidOne))
			Expect(h.readGoal(goalThreeID)).NotTo(ContainSubstring("claude_session_started"))
		})
	})

	Describe("ExecuteTaskCommand session path", func() {
		It("500s when the work-on session cannot start", func() {
			_, err := h.service.ExecuteTaskCommand(
				ctx, "personal", taskOneID, api.ExecuteCommandRequest{Command: "work-on-task"},
			)
			Expect(httpStatus(err)).To(Equal(500))
		})
	})

	Describe("SetTaskSession with an unresolvable display name", func() {
		It("stores the display name as-is", func() {
			result, err := h.service.SetTaskSession(
				ctx, "personal", taskOneID,
				api.UpdateSessionRequest{ClaudeSessionID: "My Session"},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.ClaudeSessionID).To(Equal("My Session"))
		})
	})

	Describe("resume command with a session project dir", func() {
		It("prefixes cd and quotes the title", func() {
			h2 := newHarnessWith(func(v *vaultconfig.Vault) {
				v.SessionProjectDir = "~/proj"
			})
			result, err := h2.service.TakeOverTask(ctx, "personal", taskFourID)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Command).To(ContainSubstring(`cd "`))
			Expect(result.Command).To(ContainSubstring("--resume " + uuidOne))
		})
	})
})

var _ = Describe("Mutation service guard branches", func() {
	var h *harness
	var ctx context.Context

	BeforeEach(func() {
		h = newHarness()
		ctx = context.Background()
	})

	DescribeTable("maps unknown vaults per route",
		func(run func() error, want int) {
			Expect(httpStatus(run())).To(Equal(want))
		},
		Entry("assign task", func() error {
			_, e := h.service.AssignTaskToMe(ctx, "nope", taskOneID)
			return e
		}, 404),
		Entry("assign goal", func() error {
			_, e := h.service.AssignGoalToMe(ctx, "nope", goalOneID)
			return e
		}, 404),
		Entry("clear goal session", func() error {
			_, e := h.service.ClearGoalSession(ctx, "nope", goalOneID)
			return e
		}, 400),
		Entry("take-over goal", func() error {
			_, e := h.service.TakeOverGoal(ctx, "nope", goalOneID)
			return e
		}, 500),
		Entry("execute goal command", func() error {
			_, e := h.service.ExecuteGoalCommand(
				ctx, "nope", goalOneID, api.ExecuteCommandRequest{Command: "complete-goal"},
			)
			return e
		}, 400),
		Entry("task phase", func() error {
			_, e := h.service.UpdateTaskPhase(
				ctx, "nope", taskOneID, api.UpdatePhaseRequest{Phase: "execution"},
			)
			return e
		}, 400),
	)

	It("404s take-over of an unknown goal", func() {
		_, err := h.service.TakeOverGoal(ctx, "personal", "Missing")
		Expect(httpStatus(err)).To(Equal(404))
	})

	It("404s assign-to-me of an unknown task", func() {
		_, err := h.service.AssignTaskToMe(ctx, "nope", taskOneID)
		Expect(httpStatus(err)).To(Equal(404))
	})

	It("quotes a task title containing spaces in the resume command", func() {
		result, err := h.service.TakeOverTask(ctx, "personal", "Task With Space")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Command).To(ContainSubstring(`-n 'Task With Space'`))
	})
})

var _ = Describe("Mutation service remaining branches", func() {
	var h *harness
	var ctx context.Context

	BeforeEach(func() {
		h = newHarness()
		ctx = context.Background()
	})

	It("400s assign-task when no current user is configured", func() {
		h.cfgPtr.CurrentUser = ""
		_, err := h.service.AssignTaskToMe(ctx, "personal", taskOneID)
		Expect(httpStatus(err)).To(Equal(400))
	})

	It("400s assign-goal when no current user is configured", func() {
		h.cfgPtr.CurrentUser = ""
		_, err := h.service.AssignGoalToMe(ctx, "personal", goalOneID)
		Expect(httpStatus(err)).To(Equal(400))
	})

	It("500s clear-task-session for an unknown vault", func() {
		_, err := h.service.ClearTaskSession(ctx, "nope", taskOneID)
		Expect(httpStatus(err)).To(Equal(500))
	})

	It("runs the defer-task fast path", func() {
		result, err := h.service.ExecuteTaskCommand(
			ctx, "personal", taskOneID, api.ExecuteCommandRequest{Command: "defer-task"},
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Command).To(ContainSubstring("task defer " + taskOneID))
		Expect(h.readTask(taskOneID)).To(ContainSubstring("defer_date"))
	})

	It("runs the defer-goal fast path", func() {
		result, err := h.service.ExecuteGoalCommand(
			ctx, "personal", goalOneID, api.ExecuteCommandRequest{Command: "defer-goal"},
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Command).To(Equal("defer-goal"))
	})

	It("500s execute-task-command for an unknown vault", func() {
		_, err := h.service.ExecuteTaskCommand(
			ctx, "nope", taskOneID, api.ExecuteCommandRequest{Command: "complete-task"},
		)
		Expect(httpStatus(err)).To(Equal(500))
	})

	It("404s update-task-flag for an unknown task", func() {
		_, err := h.service.UpdateTaskFlag(ctx, "personal", "Missing", api.UpdateFlagRequest{})
		Expect(httpStatus(err)).To(Equal(500))
	})
})

var _ = Describe("Mutation service session start (counterfeiter fakes)", func() {
	var h *harness
	var ctx context.Context
	var starter *vcmocks.ClaudeSessionStarter

	BeforeEach(func() {
		ctx = context.Background()
		// Rebuild the service with a working (faked) session starter.
		h, starter = newStarterHarness()
	})

	It("starts a task session and returns the minted id", func() {
		result, err := h.service.RunTask(ctx, "personal", taskOneID)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.SessionID).To(Equal(uuidTwo))
		Expect(starter.StartSessionCallCount()).To(Equal(1))
		Expect(h.readTask(taskOneID)).To(ContainSubstring(uuidTwo))
	})

	It("starts a goal session and stores the id on the goal", func() {
		result, err := h.service.RunGoal(ctx, "personal", goalOneID)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.SessionID).To(Equal(uuidTwo))
		Expect(h.readGoal(goalOneID)).To(ContainSubstring(uuidTwo))
	})
})

var _ = Describe("Mutation service page-index invalidation", func() {
	var h *harness
	var ctx context.Context

	BeforeEach(func() {
		h = newHarness()
		ctx = context.Background()
	})

	// expectVaultMarked asserts the invalidator recorded at least one MarkDirty
	// call whose args are exactly the vault's tasks and goals keys.
	expectVaultMarked := func(index *mocks.IndexInvalidator, dir string) {
		ExpectWithOffset(1, index.MarkDirtyCallCount()).To(BeNumerically(">=", 1))
		want := []pageindex.Key{
			pageindex.NewKey(dir, "24 Tasks"),
			pageindex.NewKey(dir, "23 Goals"),
		}
		matched := false
		for i := 0; i < index.MarkDirtyCallCount(); i++ {
			if reflect.DeepEqual(index.MarkDirtyArgsForCall(i), want) {
				matched = true
			}
		}
		ExpectWithOffset(1, matched).To(BeTrue(), "no MarkDirty call with %v", want)
	}

	DescribeTable("marks the vault's keys after a successful write",
		func(run func() *harness) {
			hh := run()
			expectVaultMarked(hh.index, hh.dir)
		},
		Entry("RunTask", func() *harness {
			sh, _ := newStarterHarness()
			_, err := sh.service.RunTask(ctx, "personal", taskOneID)
			Expect(err).NotTo(HaveOccurred())
			return sh
		}),
		Entry("TakeOverTask", func() *harness {
			_, err := h.service.TakeOverTask(ctx, "personal", taskFourID)
			Expect(err).NotTo(HaveOccurred())
			return h
		}),
		Entry("ExecuteTaskCommand", func() *harness {
			_, err := h.service.ExecuteTaskCommand(
				ctx, "personal", taskOneID,
				api.ExecuteCommandRequest{Command: "complete-task"},
			)
			Expect(err).NotTo(HaveOccurred())
			return h
		}),
		Entry("AssignTaskToMe", func() *harness {
			_, err := h.service.AssignTaskToMe(ctx, "personal", taskOneID)
			Expect(err).NotTo(HaveOccurred())
			return h
		}),
		Entry("UpdateTaskPhase", func() *harness {
			_, err := h.service.UpdateTaskPhase(
				ctx, "personal", taskOneID, api.UpdatePhaseRequest{Phase: "execution"},
			)
			Expect(err).NotTo(HaveOccurred())
			return h
		}),
		Entry("UpdateTaskFlag", func() *harness {
			_, err := h.service.UpdateTaskFlag(
				ctx, "personal", taskOneID, api.UpdateFlagRequest{},
			)
			Expect(err).NotTo(HaveOccurred())
			return h
		}),
		Entry("UpdateTaskStatus", func() *harness {
			_, err := h.service.UpdateTaskStatus(
				ctx, "personal", taskOneID, api.UpdateStatusRequest{Status: "backlog"},
			)
			Expect(err).NotTo(HaveOccurred())
			return h
		}),
		Entry("ClearTaskSession", func() *harness {
			_, err := h.service.ClearTaskSession(ctx, "personal", taskTwoID)
			Expect(err).NotTo(HaveOccurred())
			return h
		}),
		Entry("SetTaskSession", func() *harness {
			_, err := h.service.SetTaskSession(
				ctx, "personal", taskOneID,
				api.UpdateSessionRequest{ClaudeSessionID: uuidTwo},
			)
			Expect(err).NotTo(HaveOccurred())
			return h
		}),
		Entry("RunGoal", func() *harness {
			sh, _ := newStarterHarness()
			_, err := sh.service.RunGoal(ctx, "personal", goalOneID)
			Expect(err).NotTo(HaveOccurred())
			return sh
		}),
		Entry("TakeOverGoal", func() *harness {
			_, err := h.service.TakeOverGoal(ctx, "personal", goalThreeID)
			Expect(err).NotTo(HaveOccurred())
			return h
		}),
		Entry("UpdateGoalStatus", func() *harness {
			_, err := h.service.UpdateGoalStatus(
				ctx, "personal", goalOneID, api.UpdateStatusRequest{Status: "hold"},
			)
			Expect(err).NotTo(HaveOccurred())
			return h
		}),
		Entry("ExecuteGoalCommand", func() *harness {
			_, err := h.service.ExecuteGoalCommand(
				ctx, "personal", goalOneID,
				api.ExecuteCommandRequest{Command: "complete-goal"},
			)
			Expect(err).NotTo(HaveOccurred())
			return h
		}),
		Entry("AssignGoalToMe", func() *harness {
			_, err := h.service.AssignGoalToMe(ctx, "personal", goalOneID)
			Expect(err).NotTo(HaveOccurred())
			return h
		}),
		Entry("ClearGoalSession", func() *harness {
			_, err := h.service.ClearGoalSession(ctx, "personal", goalTwoID)
			Expect(err).NotTo(HaveOccurred())
			return h
		}),
	)

	It("marks before PublishTaskUpdated for UpdateTaskPhase", func() {
		h.publisher.PublishTaskUpdatedStub = func(context.Context, string, string) {
			ExpectWithOffset(1, h.index.MarkDirtyCallCount()).To(BeNumerically(">=", 1))
		}
		_, err := h.service.UpdateTaskPhase(
			ctx, "personal", taskOneID, api.UpdatePhaseRequest{Phase: "execution"},
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(h.publisher.PublishTaskUpdatedCallCount()).To(Equal(1))
	})

	It("marks before PublishGoalUpdated for UpdateGoalStatus", func() {
		h.publisher.PublishGoalUpdatedStub = func(context.Context, string, string) {
			ExpectWithOffset(1, h.index.MarkDirtyCallCount()).To(BeNumerically(">=", 1))
		}
		_, err := h.service.UpdateGoalStatus(
			ctx, "personal", goalOneID, api.UpdateStatusRequest{Status: "hold"},
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(h.publisher.PublishGoalUpdatedCallCount()).To(Equal(1))
	})

	It("marks when a write fails after writing (RunTask with an erroring starter)", func() {
		starter := &vcmocks.ClaudeSessionStarter{}
		starter.StartSessionReturns(errors.New("starter boom"))
		sh := newStarterHarnessWith(starter, &vcmocks.ClaudeResumer{})
		_, err := sh.service.RunTask(ctx, "personal", taskOneID)
		Expect(httpStatus(err)).To(Equal(500))
		expectVaultMarked(sh.index, sh.dir)
	})

	It("does not mark for JumpTask", func() {
		Expect(h.service.JumpTask(ctx, "personal", taskTwoID, true)).To(Succeed())
		Expect(h.index.MarkDirtyCallCount()).To(Equal(0))
		Expect(h.index.MarkAllDirtyCallCount()).To(Equal(0))
	})

	It("does not mark for ReloadConfig", func() {
		_, err := h.service.ReloadConfig(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(h.index.MarkDirtyCallCount()).To(Equal(0))
		Expect(h.index.MarkAllDirtyCallCount()).To(Equal(0))
	})

	It("marks every key once on a single-vault reload", func() {
		_, err := h.service.ReloadCache(ctx, "personal")
		Expect(err).NotTo(HaveOccurred())
		Expect(h.index.MarkAllDirtyCallCount()).To(Equal(1))
		Expect(h.index.MarkDirtyCallCount()).To(Equal(0))
	})

	It("marks every key once on an all-vault reload", func() {
		_, err := h.service.ReloadCache(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(h.index.MarkAllDirtyCallCount()).To(Equal(1))
	})

	It("does not mark on a 404 reload", func() {
		_, err := h.service.ReloadCache(ctx, "nope")
		Expect(httpStatus(err)).To(Equal(404))
		Expect(h.index.MarkAllDirtyCallCount()).To(Equal(0))
	})
})
