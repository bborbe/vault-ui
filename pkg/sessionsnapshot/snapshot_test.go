// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sessionsnapshot_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/golang/glog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/board"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/pageindex"
	"github.com/bborbe/vault-ui/pkg/sessionsnapshot"
	"github.com/bborbe/vault-ui/pkg/sessionsnapshot/mocks"
	"github.com/bborbe/vault-ui/pkg/statuscache"
)

// The generated fake must keep satisfying the interface it was generated from.
var _ sessionsnapshot.Snapshot = &mocks.Snapshot{}

const (
	liveSessionID = "11111111-1111-1111-1111-111111111111"
	otherID       = "22222222-2222-2222-2222-222222222222"
)

// baseTime is the fixed clock every spec runs against, so no spec depends on
// the wall clock.
var baseTime = time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)

// psLine renders a `ps` row for a live claude process pinned to sessionID.
func psLine(sessionID string) string {
	return "claude --resume " + sessionID
}

// countingScanner counts `ps` scans and returns a settable output/error.
type countingScanner struct {
	mu     sync.Mutex
	calls  int
	output string
	err    error
}

func (s *countingScanner) Scan(_ context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.output, s.err
}

func (s *countingScanner) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// countingRegistry counts registry reads and returns a settable set.
type countingRegistry struct {
	ids   []string
	calls int
}

func (r *countingRegistry) Get(_ context.Context) []string {
	r.calls++
	return r.ids
}

// countingProbe counts transcript probes and returns a settable mtime.
type countingProbe struct {
	mu    sync.Mutex
	calls int
	value *libtime.DateTime
}

func (p *countingProbe) Probe(_ context.Context, _, _, _ string) *libtime.DateTime {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.value
}

func (p *countingProbe) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// mutableClock is a clock a spec advances by hand.
type mutableClock struct {
	now libtime.DateTime
}

func (c *mutableClock) Now() libtime.DateTime { return c.now }

// fakeWaiter drives the refresh interval: it records every duration it was
// asked to wait for and fails (as a cancelled context does) after `limit`
// waits, so Run terminates.
type fakeWaiter struct {
	mu        sync.Mutex
	calls     int
	durations []libtime.Duration
	limit     int
}

func (w *fakeWaiter) Wait(_ context.Context, duration libtime.Duration) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	w.durations = append(w.durations, duration)
	if w.calls > w.limit {
		return context.Canceled
	}
	return nil
}

func (w *fakeWaiter) observed() (int, []libtime.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls, append([]libtime.Duration{}, w.durations...)
}

// newSnapshot builds a snapshot over the given fakes with a fixed clock.
func newSnapshot(
	scanner *countingScanner,
	registry *countingRegistry,
	probe *countingProbe,
	clock libtime.CurrentDateTimeGetter,
	waiter libtime.WaiterDuration,
) sessionsnapshot.Snapshot {
	return sessionsnapshot.NewSnapshot(sessionsnapshot.Params{
		Registry: registry.Get,
		Scanner:  scanner.Scan,
		Probe:    probe.Probe,
		Clock:    clock,
		Waiter:   waiter,
	})
}

func fixedClock() libtime.CurrentDateTimeGetter {
	return libtime.CurrentDateTimeGetterFunc(func() libtime.DateTime {
		return libtime.DateTime(baseTime).UTC()
	})
}

// captureStderr runs fn with os.Stderr redirected into a pipe and returns what
// was written. glog's stderr sink resolves os.Stderr at write time, so an
// error-level line lands in the pipe.
func captureStderr(fn func()) string {
	old := os.Stderr
	reader, writer, err := os.Pipe()
	Expect(err).NotTo(HaveOccurred())
	os.Stderr = writer

	fn()
	glog.Flush()
	Expect(writer.Close()).To(Succeed())
	os.Stderr = old

	data, err := io.ReadAll(reader)
	Expect(err).NotTo(HaveOccurred())
	Expect(reader.Close()).To(Succeed())
	return string(data)
}

var _ = Describe("SessionSnapshot", func() {
	var (
		ctx      context.Context
		scanner  *countingScanner
		registry *countingRegistry
		probe    *countingProbe
		snapshot sessionsnapshot.Snapshot
	)

	BeforeEach(func() {
		ctx = context.Background()
		scanner = &countingScanner{output: psLine(liveSessionID)}
		registry = &countingRegistry{ids: []string{otherID}}
		probe = &countingProbe{}
		snapshot = newSnapshot(scanner, registry, probe, fixedClock(), nil)
	})

	It("reads the registry and the process table once per refresh", func() {
		Expect(snapshot.RefreshOnce(ctx)).To(Succeed())

		Expect(scanner.callCount()).To(Equal(1))
		Expect(registry.calls).To(Equal(1))
		Expect(snapshot.ResumeSessionIDs(ctx)).To(Equal([]string{liveSessionID}))
		Expect(snapshot.RegistrySessionIDs(ctx)).To(Equal([]string{otherID}))
		Expect(snapshot.Generation()).To(Equal(uint64(1)))
	})

	It("returns non-nil, non-aliasing slices", func() {
		Expect(snapshot.RefreshOnce(ctx)).To(Succeed())

		first := snapshot.RegistrySessionIDs(ctx)
		Expect(first).NotTo(BeNil())
		first[0] = "mutated"
		Expect(snapshot.RegistrySessionIDs(ctx)).To(Equal([]string{otherID}))

		resume := snapshot.ResumeSessionIDs(ctx)
		Expect(resume).NotTo(BeNil())
		resume[0] = "mutated"
		Expect(snapshot.ResumeSessionIDs(ctx)).To(Equal([]string{liveSessionID}))
	})

	It("returns empty slices, never nil, before the first refresh", func() {
		Expect(snapshot.RegistrySessionIDs(ctx)).NotTo(BeNil())
		Expect(snapshot.RegistrySessionIDs(ctx)).To(BeEmpty())
		Expect(snapshot.ResumeSessionIDs(ctx)).NotTo(BeNil())
		Expect(snapshot.ResumeSessionIDs(ctx)).To(BeEmpty())
		Expect(snapshot.Generation()).To(Equal(uint64(0)))
	})

	// AC2: a request path read spawns no `ps`. N reads between refreshes add
	// exactly zero scans; the count rises only across a refresh.
	It("spawns no ps scan on the read path (AC2)", func() {
		Expect(snapshot.RefreshOnce(ctx)).To(Succeed())
		Expect(scanner.callCount()).To(Equal(1))

		for range 50 {
			snapshot.ResumeSessionIDs(ctx)
			snapshot.RegistrySessionIDs(ctx)
		}
		Expect(scanner.callCount()).To(Equal(1))

		Expect(snapshot.RefreshOnce(ctx)).To(Succeed())
		Expect(scanner.callCount()).To(Equal(2))
	})

	// AC8: the added timer is slow, and Run fires exactly one refresh per
	// SessionRefreshInterval.
	It("refreshes once per SessionRefreshInterval (AC8)", func() {
		Expect(sessionsnapshot.SessionRefreshInterval).To(BeNumerically(">=", 60*time.Second))

		waiter := &fakeWaiter{limit: 2}
		running := newSnapshot(scanner, registry, probe, fixedClock(), waiter)

		Expect(running.Run(ctx)).To(Succeed())

		// One initial refresh plus one per successful wait, and the third wait
		// reports cancellation.
		Expect(scanner.callCount()).To(Equal(3))
		Expect(running.Generation()).To(Equal(uint64(3)))

		calls, durations := waiter.observed()
		Expect(calls).To(Equal(3))
		for _, duration := range durations {
			Expect(duration).To(Equal(libtime.Duration(sessionsnapshot.SessionRefreshInterval)))
		}
	})

	It("keeps the previous values when a refresh fails", func() {
		Expect(snapshot.RefreshOnce(ctx)).To(Succeed())
		before := snapshot.Generation()

		cause := errors.New("ps exploded")
		scanner.err = cause

		var returned error
		output := captureStderr(func() {
			returned = snapshot.RefreshOnce(ctx)
		})

		Expect(returned).To(HaveOccurred())
		Expect(errors.Is(returned, cause)).To(BeTrue())
		Expect(output).To(ContainSubstring("refresh failed"))
		Expect(output).To(ContainSubstring(sessionsnapshot.SessionRefreshInterval.String()))
		Expect(output).To(ContainSubstring("ps exploded"))

		// The previous values stay served and Generation does not advance.
		Expect(snapshot.RegistrySessionIDs(ctx)).To(Equal([]string{otherID}))
		Expect(snapshot.ResumeSessionIDs(ctx)).To(Equal([]string{liveSessionID}))
		Expect(snapshot.Generation()).To(Equal(before))
	})

	It("advances Generation by one per successful refresh only", func() {
		Expect(snapshot.Generation()).To(Equal(uint64(0)))
		Expect(snapshot.RefreshOnce(ctx)).To(Succeed())
		Expect(snapshot.Generation()).To(Equal(uint64(1)))
		Expect(snapshot.RefreshOnce(ctx)).To(Succeed())
		Expect(snapshot.Generation()).To(Equal(uint64(2)))

		scanner.err = errors.New("boom")
		Expect(snapshot.RefreshOnce(ctx)).NotTo(Succeed())
		Expect(snapshot.Generation()).To(Equal(uint64(2)))
	})

	Describe("TranscriptMtime", func() {
		var (
			projectDir   string
			projectsRoot string
		)

		BeforeEach(func() {
			projectDir = "/vault/projects/-vault"
			projectsRoot = "/vault/projects"
		})

		It("returns nil for a blank session id without probing", func() {
			Expect(snapshot.TranscriptMtime(ctx, "", projectDir, projectsRoot)).To(BeNil())
			Expect(probe.callCount()).To(Equal(0))
		})

		It("probes a missing transcript once per refresh window and caches the nil", func() {
			Expect(snapshot.RefreshOnce(ctx)).To(Succeed())

			Expect(snapshot.TranscriptMtime(ctx, liveSessionID, projectDir, projectsRoot)).To(BeNil())
			Expect(snapshot.TranscriptMtime(ctx, liveSessionID, projectDir, projectsRoot)).To(BeNil())
			Expect(probe.callCount()).To(Equal(1))
		})

		// DB4: at most one probe per task per refresh, never one per request.
		It("probes at most once per task per refresh (DB4)", func() {
			mtime := libtime.DateTime(baseTime.Add(-time.Minute)).UTC()
			probe.value = mtime.Ptr()
			Expect(snapshot.RefreshOnce(ctx)).To(Succeed())

			Expect(snapshot.TranscriptMtime(ctx, liveSessionID, projectDir, projectsRoot)).
				To(Equal(mtime.Ptr()))
			Expect(snapshot.TranscriptMtime(ctx, liveSessionID, projectDir, projectsRoot)).
				To(Equal(mtime.Ptr()))
			Expect(probe.callCount()).To(Equal(1))

			// A different session is a different cache entry.
			Expect(snapshot.TranscriptMtime(ctx, otherID, projectDir, projectsRoot)).
				To(Equal(mtime.Ptr()))
			Expect(probe.callCount()).To(Equal(2))
		})

		It("probes again after a refresh publishes a new window", func() {
			clock := &mutableClock{now: libtime.DateTime(baseTime).UTC()}
			snapshot = newSnapshot(scanner, registry, probe, clock, nil)
			probe.value = libtime.DateTime(baseTime.Add(-time.Minute)).UTC().Ptr()

			Expect(snapshot.RefreshOnce(ctx)).To(Succeed())
			snapshot.TranscriptMtime(ctx, liveSessionID, projectDir, projectsRoot)
			Expect(probe.callCount()).To(Equal(1))

			clock.now = libtime.DateTime(baseTime.Add(2 * time.Minute)).UTC()
			Expect(snapshot.RefreshOnce(ctx)).To(Succeed())
			snapshot.TranscriptMtime(ctx, liveSessionID, projectDir, projectsRoot)
			Expect(probe.callCount()).To(Equal(2))
		})

		It("falls back to activity.TranscriptMtime when no probe is injected", func() {
			tmp := GinkgoT().TempDir()
			root := filepath.Join(tmp, "projects")
			Expect(os.MkdirAll(filepath.Join(root, "-vault"), 0o750)).To(Succeed())
			path := filepath.Join(root, "-vault", liveSessionID+".jsonl")
			Expect(os.WriteFile(path, []byte("{}\n"), 0o600)).To(Succeed())

			bare := sessionsnapshot.NewSnapshot(sessionsnapshot.Params{
				Registry: registry.Get,
				Scanner:  scanner.Scan,
				Clock:    fixedClock(),
			})
			Expect(bare.RefreshOnce(ctx)).To(Succeed())

			Expect(
				bare.TranscriptMtime(ctx, liveSessionID, filepath.Join(root, "-vault"), root),
			).NotTo(BeNil())
			Expect(bare.TranscriptMtime(ctx, "no-such-session", filepath.Join(root, "-vault"), root)).
				To(BeNil())
		})
	})

	// AC5 (container proxy): a refresh re-reads the live set, and a board built
	// on the snapshot stops classifying an ended session as live.
	Describe("re-reading the live set", func() {
		It("serves the new ids after a second refresh and flips the board", func() {
			staleMtime := libtime.DateTime(baseTime.Add(-3 * time.Hour)).UTC()
			probe.value = staleMtime.Ptr()
			registry.ids = []string{liveSessionID}

			Expect(snapshot.RefreshOnce(ctx)).To(Succeed())
			Expect(snapshot.Generation()).To(Equal(uint64(1)))

			service := newSnapshotBoard(snapshot)
			Expect(classifyFirstTask(ctx, service)).To(Equal("live"))

			// The session ends: it leaves the registry and the process table.
			registry.ids = []string{}
			scanner.output = ""
			Expect(snapshot.RefreshOnce(ctx)).To(Succeed())

			Expect(snapshot.RegistrySessionIDs(ctx)).To(BeEmpty())
			Expect(snapshot.ResumeSessionIDs(ctx)).To(BeEmpty())
			Expect(snapshot.Generation()).To(Equal(uint64(2)))
			Expect(classifyFirstTask(ctx, service)).To(Equal("quiet"))
		})
	})
})

// newSnapshotBoard builds a board whose session-derived fields come from the
// given snapshot, so a spec can observe the classification the board renders.
func newSnapshotBoard(snapshot sessionsnapshot.Snapshot) board.Board {
	return board.New(board.Deps{
		Vaults:   staticVaults{},
		Ops:      staticOps{},
		Cache:    statuscache.NewCache(),
		Launch:   launchregistry.NewRegistry(),
		Clock:    fixedClock(),
		Signals:  snapshot,
		Sessions: snapshot,
		Index:    staticIndex{},
	})
}

// staticIndex is a page-index revision source that never moves: this board is
// built fresh per spec, so the store's first read always builds and no read
// rebuilds behind the spec's back.
type staticIndex struct{}

func (staticIndex) Revision(pageindex.Key) uint64 { return 0 }

// classifyFirstTask lists the single fixture task and returns its rendered
// session state.
func classifyFirstTask(ctx context.Context, service board.Board) string {
	responses, err := service.ListTasks(ctx, board.TaskQuery{UpcomingHours: 8})
	Expect(err).NotTo(HaveOccurred())
	Expect(responses).To(HaveLen(1))
	Expect(responses[0].SessionState).NotTo(BeNil())
	return *responses[0].SessionState
}

var sessionVault = board.Vault{
	Name:         "personal",
	VaultName:    "Personal",
	Path:         "/vault",
	TasksFolder:  "24 Tasks",
	GoalsFolder:  "23 Goals",
	TopicsFolder: "23 Topics",
}

type staticVaults struct{}

func (staticVaults) Vaults(_ context.Context) ([]board.Vault, error) {
	return []board.Vault{sessionVault}, nil
}

type staticList struct{}

func (staticList) Execute(
	_ context.Context,
	_, _, _ string,
	_ []string,
	_ bool,
	_, _ string,
) ([]ops.TaskListItem, error) {
	return []ops.TaskListItem{{
		Name:            "Live",
		Status:          "todo",
		ClaudeSessionID: liveSessionID,
	}}, nil
}

type staticOps struct{}

func (staticOps) List(_ board.Vault) ops.ListOperation { return staticList{} }

func (staticOps) TopicShow(_ board.Vault) ops.EntityShowOperation { return nil }
