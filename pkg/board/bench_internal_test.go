// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/ops"

	"github.com/bborbe/vault-ui/pkg/activity"
	"github.com/bborbe/vault-ui/pkg/cleanup"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/pageindex"
	pageindexmocks "github.com/bborbe/vault-ui/pkg/pageindex/mocks"
	"github.com/bborbe/vault-ui/pkg/session"
)

// benchItemCount is the fixture size. It is well above the two thousand items
// the attribution criterion requires, so every section of the rebuild does real
// work and the whole-rebuild figure is not dominated by its own setup.
const benchItemCount = 2500

const (
	// benchCompletedItem is the one fixture item the status cache reports as
	// completed, so uncompletedBlockers drops exactly one of every item's two
	// blockers.
	benchCompletedItem = "Bench Item 0"
	// benchStartedItem is the one fixture item carrying a session-started marker.
	benchStartedItem = "Bench Item 3"
	// benchLiveSessionID is the one session id the fixture's registry reports
	// live, so the classifier, the registry location and the transcript probe all
	// have a matching item and a non-matching remainder.
	benchLiveSessionID = "11111111-1111-1111-1111-111111111111"
	// benchResumeID is the fixture's pinned resume process id.
	benchResumeID = "22222222-2222-2222-2222-222222222222"

	benchHomeDir       = "/nonexistent-bench-home"
	benchRegistryLoc   = "local"
	benchStartedMarker = "2026-01-02T03:04:05Z"
	benchModifiedDate  = "2026-01-02T03:04:05Z"

	// benchPageContent is a real Open Questions section, so the Open Questions
	// section runs storage.ParseOpenQuestions against it rather than reading a
	// hand-built slice the benchmark itself typed.
	benchPageContent = "# Task\n\n## Open Questions\n\n" +
		"- Which vault?\n- Answered already → **personal**\n\n" +
		"## Notes\n\n- not a question\n"
)

var (
	benchNow   = time.Date(2026, time.January, 2, 12, 0, 0, 0, time.UTC)
	benchMtime = libtime.DateTime(time.Date(2026, time.January, 2, 11, 0, 0, 0, time.UTC))
	benchVault = Vault{
		Name:              "bench",
		VaultName:         "Bench",
		Path:              "/vault",
		TasksFolder:       "24 Tasks",
		GoalsFolder:       "23 Goals",
		TopicsFolder:      "23 Topics",
		SessionProjectDir: "/vault",
	}
)

// benchSink keeps every sub-benchmark's result reachable, so the compiler
// cannot elide the work being measured. Writing a field of it allocates
// nothing.
var benchSink struct {
	items     []ops.TaskListItem
	rows      []taskSnapshotRow
	names     []string
	questions []domain.OpenQuestion
	started   *string
	state     *string
	location  string
	date      *libtime.DateTime
	n         int
	body      TaskListBody
}

// benchItemName is the fixture's naming scheme: a distinct, stable name per
// index, which is also the page name the Open Questions seam is keyed by.
func benchItemName(index int) string {
	return "Bench Item " + strconv.Itoa(index)
}

// benchItems builds the fixture's task items. Each carries the fields the
// rebuild's sections read — a distinct name, a status, a two-entry BlockedBy
// naming one completed and one uncompleted fixture item, a session id and a
// modified date — so every section does real work on every item.
func benchItems() []ops.TaskListItem {
	items := make([]ops.TaskListItem, 0, benchItemCount)
	for i := 0; i < benchItemCount; i++ {
		status := "todo"
		if i == 0 {
			status = "completed"
		}
		sessionID := "bench-session-" + strconv.Itoa(i)
		if i == 1 {
			sessionID = benchLiveSessionID
		}
		// The second blocker is always a non-completed item: the range 1..n-1
		// excludes index 0, the one completed item.
		blocker := (i+1)%(benchItemCount-1) + 1
		items = append(items, ops.TaskListItem{
			Name:            benchItemName(i),
			Status:          status,
			Vault:           benchVault.Name,
			ClaudeSessionID: sessionID,
			ModifiedDate:    benchModifiedDate,
			BlockedBy: []string{
				"[[" + benchCompletedItem + "]]",
				"[[" + benchItemName(blocker) + "]]",
			},
		})
	}
	return items
}

// benchPages builds one page per fixture item, each carrying a small Open
// Questions section, so openQuestionsByTask parses real markdown for every
// item.
func benchPages(items []ops.TaskListItem) []*domain.Page {
	pages := make([]*domain.Page, 0, len(items))
	for _, item := range items {
		pages = append(pages, domain.NewPage(
			map[string]any{"status": item.Status},
			domain.FileMetadata{Name: item.Name},
			domain.Content(benchPageContent),
		))
	}
	return pages
}

// benchVaults returns the single fixture vault.
type benchVaults struct{}

func (benchVaults) Vaults(context.Context) ([]Vault, error) {
	return []Vault{benchVault}, nil
}

// benchList is the fixture's list operation: it returns the fixture items and
// ignores the pages dir, so the vault-list section performs no I/O.
type benchList struct {
	items []ops.TaskListItem
}

func (l benchList) Execute(
	_ context.Context,
	_, _, _ string,
	_ []string,
	_ bool,
	_, _ string,
) ([]ops.TaskListItem, error) {
	return l.items, nil
}

// benchOps is the fixture's OpsProvider.
type benchOps struct {
	items []ops.TaskListItem
}

func (o benchOps) List(Vault) ops.ListOperation { return benchList{items: o.items} }

func (benchOps) TopicShow(Vault) ops.EntityShowOperation { return nil }

// benchCache is the fixture's status cache: the one completed fixture item reads
// "completed", every other item reads "todo", and one item carries a fixed
// session-started marker.
type benchCache struct{}

func (benchCache) LoadVault(_, _, _ string) error { return nil }

func (benchCache) GetStatus(_, itemID string) (string, bool) {
	if itemID == benchCompletedItem {
		return "completed", true
	}
	return "todo", true
}

func (benchCache) GetSessionStarted(_, itemID string) (string, bool) {
	if itemID == benchStartedItem {
		return benchStartedMarker, true
	}
	return "", false
}

func (benchCache) Count(string) int { return benchItemCount }

func (benchCache) Invalidate(_, _ string) {}

// benchSignals is the fixture's session-signal source: one live registry id, a
// fixed location for it, a known live set and one pinned resume id.
type benchSignals struct{}

func (benchSignals) RegistrySessionIDs(context.Context) []string {
	return []string{benchLiveSessionID}
}

func (benchSignals) RegistryLocation(_ context.Context, sessionID string) string {
	if sessionID == benchLiveSessionID {
		return benchRegistryLoc
	}
	return ""
}

func (benchSignals) LiveIDsKnown() bool { return true }

func (benchSignals) ResumeSessionIDs(context.Context) []string {
	return []string{benchResumeID}
}

// benchProbe is the fixture's transcript probe: a fixed mtime for the live
// session id, nil otherwise, and a fixed generation. It never touches the
// filesystem.
type benchProbe struct{}

func (benchProbe) TranscriptMtime(
	_ context.Context, sessionID, _, _ string,
) *libtime.DateTime {
	if sessionID == benchLiveSessionID {
		return &benchMtime
	}
	return nil
}

func (benchProbe) Generation() uint64 { return 1 }

// benchIndex is the fixture's IndexRevisions: a fixed revision that bounds
// nothing. buildTaskRows never reads it — the store does — but Deps requires
// it.
type benchIndex struct{}

func (benchIndex) Revision(pageindex.Key) uint64 { return 1 }

func (benchIndex) ChangedPagesSince(pageindex.Key, uint64) ([]string, bool) {
	return nil, false
}

func (benchIndex) ListPages(context.Context, string, string) ([]*domain.Page, error) {
	return nil, nil
}

// benchBoard builds the board the benchmark measures, on an in-memory fixture
// of benchItemCount task items. It needs no real vault and touches no
// filesystem: every dependency is an in-memory fake, and HomeDir names a path
// that does not exist because cleanup.DeriveClaudeProjectDir is pure string
// work.
//
// The launch registry is a real one: sessionStarted reads it on every item, and
// a nil Launch in Deps would panic on the first whole-rebuild iteration.
func benchBoard(tb testing.TB) *board {
	tb.Helper()
	items := benchItems()
	pageIndex := &pageindexmocks.PageIndex{}
	pageIndex.ListPagesReturns(benchPages(items), nil)
	return New(Deps{
		Vaults: benchVaults{},
		Ops:    benchOps{items: items},
		Cache:  benchCache{},
		Launch: launchregistry.NewRegistry(),
		Clock: libtime.CurrentDateTimeGetterFunc(func() libtime.DateTime {
			return libtime.DateTime(benchNow)
		}),
		Signals:   benchSignals{},
		Sessions:  benchProbe{},
		Index:     benchIndex{},
		PageIndex: pageIndex,
		HomeDir:   benchHomeDir,
	}).(*board)
}

// BenchmarkBuildTaskRows attributes the task-list rebuild's allocation to the
// rebuild's sections. Each sub-benchmark measures one section in isolation on
// the same 2,500-item fixture; "whole" measures a genuine end-to-end
// buildTaskRows call — never a composition of the section helpers — and
// "residual" measures the work the whole function does that no section models:
// the setup hoisted out of the per-task loop and the row slice's own backing
// array.
//
// No numeric relationship between the figures is asserted anywhere. An isolated
// section differs from the whole in shared allocations, escape and inlining, so
// the sections need not sum to the whole even in a correct implementation; the
// residual is recorded alongside them rather than compared against them.
func BenchmarkBuildTaskRows(b *testing.B) {
	ctx := context.Background()
	items := benchItems()
	bd := benchBoard(b)
	vault := benchVault

	projectDir := cleanup.DeriveClaudeProjectDir(bd.homeDir, vault.Path, vault.SessionProjectDir)
	projectsRoot := filepath.Join(bd.homeDir, ".claude", "projects")
	registryIDs := bd.signals.RegistrySessionIDs(ctx)
	liveIDsUnknown := !bd.signals.LiveIDsKnown()
	resumeIDs := bd.signals.ResumeSessionIDs(ctx)

	// vault_list is the vault-cli list walk the rebuild starts with.
	b.Run("vault_list", func(b *testing.B) {
		op := bd.ops.List(vault)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			listed, err := op.Execute(
				ctx, vault.Path, vault.Name, vault.TasksFolder, nil, true, "", "",
			)
			if err != nil {
				b.Fatal(err)
			}
			benchSink.items = listed
		}
	})

	// row_construction is the per-row construction the build loop performs
	// outside the section helpers.
	b.Run("row_construction", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rows := make([]taskSnapshotRow, 0, len(items))
			for _, item := range items {
				rows = append(rows, taskSnapshotRow{item: item, vault: vault})
			}
			benchSink.rows = rows
		}
	})

	// blockers is the uncompleted-blocker derivation for every item.
	b.Run("blockers", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for _, item := range items {
				benchSink.names = bd.uncompletedBlockers(vault.Name, item.BlockedBy)
			}
		}
	})

	// session_classification is the classifier for every item, with the params
	// built exactly as buildTaskRows builds them.
	b.Run("session_classification", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for _, item := range items {
				state := session.ClassifySessionState(ctx, session.ClassifyParams{
					SessionID:          item.ClaudeSessionID,
					ProjectDir:         projectDir,
					ProjectsRoot:       projectsRoot,
					Now:                bd.clock.Now().UTC(),
					LiveWindow:         session.DefaultLiveWindow,
					ResumeSessionIDs:   resumeIDs,
					RegistrySessionIDs: registryIDs,
					LiveIDsUnknown:     liveIDsUnknown,
					TranscriptMtime:    bd.transcriptProbe(),
				})
				benchSink.state = sessionStatePtr(state)
			}
		}
	})

	// session_started is the session-started marker for every item.
	b.Run("session_started", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for _, item := range items {
				benchSink.started = bd.sessionStarted(vault.Name, item.Name)
			}
		}
	})

	// registry_location is the store's location read for every item.
	b.Run("registry_location", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for _, item := range items {
				benchSink.location = bd.signals.RegistryLocation(ctx, item.ClaudeSessionID)
			}
		}
	})

	// activity_date is the transcript-aware activity date for every item.
	b.Run("activity_date", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for _, item := range items {
				benchSink.date = activity.ComputeActivityDateWith(
					ctx,
					bd.transcriptProbe(),
					parseDateTime(item.ModifiedDate),
					item.ClaudeSessionID,
					projectDir,
					projectsRoot,
				)
			}
		}
	})

	// open_questions is the Open Questions section: one page listing per
	// iteration, exactly as buildTaskRows makes it, then the lookup per item.
	b.Run("open_questions", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			byName := bd.openQuestionsByTask(ctx, vault)
			for _, item := range items {
				benchSink.questions = openQuestionsFor(byName, item.Name)
			}
		}
	})

	// whole is a genuine end-to-end buildTaskRows call, never composed from the
	// section helpers.
	b.Run("whole", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rows, err := bd.buildTaskRows(ctx, vault)
			if err != nil {
				b.Fatal(err)
			}
			benchSink.rows = rows
		}
	})

	// residual is the allocation the whole function makes that no section
	// models: the setup hoisted out of the per-task loop — the derived project
	// dir, the projects root and the three session-signal reads — plus the row
	// slice's own backing array.
	b.Run("residual", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			derivedDir := cleanup.DeriveClaudeProjectDir(
				bd.homeDir, vault.Path, vault.SessionProjectDir,
			)
			derivedRoot := filepath.Join(bd.homeDir, ".claude", "projects")
			derivedRegistryIDs := bd.signals.RegistrySessionIDs(ctx)
			derivedUnknown := !bd.signals.LiveIDsKnown()
			derivedResumeIDs := bd.signals.ResumeSessionIDs(ctx)
			rows := make([]taskSnapshotRow, 0, len(items))
			benchSink.n = len(derivedDir) + len(derivedRoot) +
				len(derivedRegistryIDs) + len(derivedResumeIDs) + len(rows)
			if derivedUnknown {
				benchSink.n++
			}
		}
	})
}

// BenchmarkListTasksBodyWarm measures one warm GET /api/tasks body read: the
// per-request cost once the body cache holds the query's body, as distinct from
// BenchmarkBuildTaskRows' rebuild cost. The first call builds and holds the
// body; the measured loop is the cache-hit path a served request takes.
func BenchmarkListTasksBodyWarm(b *testing.B) {
	ctx := context.Background()
	bd := benchBoard(b)
	query := TaskQuery{UpcomingHours: 8}

	if _, err := bd.ListTasksBody(ctx, query); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		body, err := bd.ListTasksBody(ctx, query)
		if err != nil {
			b.Fatal(err)
		}
		benchSink.body = body
	}
}

// BenchmarkTaskSnapshotRowsWarm measures the snapshot-rows read in isolation on
// the warm store, so the per-request cost it contributes can be attributed.
func BenchmarkTaskSnapshotRowsWarm(b *testing.B) {
	ctx := context.Background()
	bd := benchBoard(b)
	query := TaskQuery{UpcomingHours: 8}

	if _, err := bd.taskSnapshotRows(ctx, query); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := bd.taskSnapshotRows(ctx, query)
		if err != nil {
			b.Fatal(err)
		}
		benchSink.rows = rows
	}
}
