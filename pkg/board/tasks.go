// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board

import (
	"context"
	"path/filepath"
	"time"

	"github.com/bborbe/errors"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/bborbe/vault-cli/pkg/storage"
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/activity"
	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/cleanup"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/session"
)

// defaultStatuses is the effective status set when no status[] is supplied.
var defaultStatuses = []string{"todo", "next", "in_progress", "hold", "completed"}

// validPhases is the phase enum; an unrecognized phase is treated as todo.
var validPhases = []string{
	"todo", "planning", "in_progress", "execution", "ai_review", "human_review", "done",
}

// taskRow is one vault-cli list item plus its derived fields.
type taskRow struct {
	item              ops.TaskListItem
	upcoming          bool
	recentlyCompleted bool
	phaseOverride     *string
	blockers          []string
	blocked           bool
	started           *string
	sessionState      *string
	openQuestions     []domain.OpenQuestion
}

// ListTasks reproduces GET /api/tasks from the precomputed task-list snapshot.
// The request path performs no vault read, no transcript probe and no process
// spawn: it applies the query filters and the time-dependent visibility rules to
// the already-built rows and renders them.
func (b *board) ListTasks(ctx context.Context, query TaskQuery) ([]api.TaskResponse, error) {
	all, err := b.vaults.Vaults(ctx)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "list vaults")
	}
	selected := b.selectVaults(all, query.Vaults)

	statusFilter := flattenFilter(query.Statuses)
	effectiveStatus := statusFilter
	if effectiveStatus == nil {
		effectiveStatus = defaultStatuses
	}
	phaseFilter := flattenFilter(query.Phases)
	assigneeFilter := flattenAssigneeFilter(query.Assignees)
	goalFilter := flattenFilter(query.Goals)

	now := b.clock.Now().UTC().Time()
	cutoff := now.Add(time.Duration(query.UpcomingHours) * time.Hour)
	lookback := now.Add(-LookbackHours * time.Hour)

	responses := make([]api.TaskResponse, 0, len(selected))
	for _, vault := range selected {
		rows, snapshotErr := b.snapshot.List(ctx, vault)
		if snapshotErr != nil {
			return nil, snapshotErr
		}
		for _, row := range rows {
			if !hasString(effectiveStatus, row.item.Status) {
				continue
			}
			if !phaseMatches(row.item.Phase, phaseFilter) {
				continue
			}
			if !assigneeMatches(row.item.Assignee, assigneeFilter) {
				continue
			}
			if !goalMatches(goalsValue(row.item.Goals), goalFilter) {
				continue
			}
			visibleTask, visible, visibleErr := b.visibleRow(ctx, row.item, now, cutoff, lookback)
			if visibleErr != nil {
				return nil, errors.Wrapf(ctx, visibleErr, "resolve visibility for %s", row.item.Name)
			}
			if !visible {
				continue
			}
			visibleTask.blockers = row.blockers
			visibleTask.blocked = row.blocked
			visibleTask.started = row.started
			visibleTask.sessionState = row.sessionState
			visibleTask.openQuestions = row.openQuestions
			responses = append(responses, b.taskResponse(vault, visibleTask, row.activityDate))
		}
	}

	if query.SessionLive {
		live := make([]api.TaskResponse, 0, len(responses))
		for _, response := range responses {
			if response.SessionState != nil && *response.SessionState == string(session.SessionStateLive) {
				live = append(live, response)
			}
		}
		responses = live
	}
	return responses, nil
}

// ListTasksBody renders the GET /api/tasks response for the query from the body
// cache, which builds it once per (snapshot generation, query) and holds the
// identity and gzip forms. A hit is served the stored bytes with no projection
// and no marshal.
//
// It reads the snapshot for the query's vaults before consulting the cache. The
// snapshot generation the cache keys on only moves when a read rebuilds a key,
// and a cache hit would never make that read: an external page edit or a new
// session snapshot would otherwise leave a held body serving indefinitely. The
// read is a per-vault revision check that returns the published rows unchanged
// when nothing moved, so it rebuilds only what the page index has marked — the
// same read ListTasks makes.
func (b *board) ListTasksBody(ctx context.Context, query TaskQuery) (TaskListBody, error) {
	if _, err := b.taskSnapshotRows(ctx, query); err != nil {
		return TaskListBody{}, err
	}
	return b.bodies.Get(ctx, query)
}

// taskSnapshotRows returns the snapshot's rows for the query's selected vaults,
// concatenated, before the status filter and visibleRow drop any. The body
// cache reads it to derive its clock boundary, so a row the visibility filter
// drops still contributes its entry instant.
//
// It makes the same vault selection ListTasks makes and reads the same
// snapshot, so it adds no vault I/O: the snapshot List is served from the
// published rows.
func (b *board) taskSnapshotRows(
	ctx context.Context,
	query TaskQuery,
) ([]taskSnapshotRow, error) {
	all, err := b.vaults.Vaults(ctx)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "list vaults")
	}
	selected := b.selectVaults(all, query.Vaults)

	rows := make([]taskSnapshotRow, 0, len(selected))
	for _, vault := range selected {
		vaultRows, snapshotErr := b.snapshot.List(ctx, vault)
		if snapshotErr != nil {
			return nil, snapshotErr
		}
		rows = append(rows, vaultRows...)
	}
	return rows, nil
}

// buildTaskRows lists the vault's tasks and precomputes every field that needs
// I/O: the uncompleted blockers, the blocked flag, the session-started marker,
// the classified session state and the activity date. It applies none of the
// request-time filters, so one build serves every query.
func (b *board) buildTaskRows(ctx context.Context, vault Vault) ([]taskSnapshotRow, error) {
	items, err := b.ops.List(vault).Execute(
		ctx, vault.Path, vault.Name, vault.TasksFolder, nil, true, "", "",
	)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "list tasks for vault %s", vault.Name)
	}

	projectDir := cleanup.DeriveClaudeProjectDir(b.homeDir, vault.Path, vault.SessionProjectDir)
	projectsRoot := filepath.Join(b.homeDir, ".claude", "projects")

	registryIDs := b.signals.RegistrySessionIDs(ctx)
	liveIDsUnknown := !b.signals.LiveIDsKnown()
	resumeIDs := b.signals.ResumeSessionIDs(ctx)

	// One ListPages for the whole vault, attached to the rows so the request
	// path never reads a page — the same precompute the other fields get.
	openQuestions := b.openQuestionsByTask(ctx, vault)

	rows := make([]taskSnapshotRow, 0, len(items))
	for _, item := range items {
		blockers := b.uncompletedBlockers(vault.Name, item.BlockedBy)
		state := session.ClassifySessionState(ctx, session.ClassifyParams{
			SessionID:          item.ClaudeSessionID,
			ProjectDir:         projectDir,
			ProjectsRoot:       projectsRoot,
			Now:                b.clock.Now().UTC(),
			LiveWindow:         session.DefaultLiveWindow,
			ResumeSessionIDs:   resumeIDs,
			RegistrySessionIDs: registryIDs,
			LiveIDsUnknown:     liveIDsUnknown,
			TranscriptMtime:    b.transcriptProbe(),
		})
		rows = append(rows, taskSnapshotRow{
			item:         item,
			vault:        vault,
			blockers:     blockers,
			blocked:      len(blockers) > 0,
			started:      b.sessionStarted(vault.Name, item.Name),
			sessionState: sessionStatePtr(state),
			activityDate: activity.ComputeActivityDateWith(
				ctx,
				b.transcriptProbe(),
				parseDateTime(item.ModifiedDate),
				item.ClaudeSessionID,
				projectDir,
				projectsRoot,
			),
			openQuestions: openQuestionsFor(openQuestions, item.Name),
		})
	}
	return rows, nil
}

// refreshTaskRows re-derives the session-dependent fields of rows that were
// already built, so a session-snapshot move does not pay for the page-derived
// work a second time.
//
// Three fields can move with the session snapshot and nothing else does: the
// classified session state and the activity date, which read the transcript
// probe and the live id sets, and the session-started marker, which is what the
// old unconditional rebuild refreshed on every tick. The vault list, the
// blockers and the Open Questions sections are page-derived and are carried
// over untouched — re-deriving those is what made a session move cost as much
// as a page move.
//
// It returns a new slice: the rows it is given are shared with every reader, so
// each is copied before a field is written.
func (b *board) refreshTaskRows(
	ctx context.Context,
	vault Vault,
	rows []taskSnapshotRow,
) ([]taskSnapshotRow, error) {
	projectDir := cleanup.DeriveClaudeProjectDir(b.homeDir, vault.Path, vault.SessionProjectDir)
	projectsRoot := filepath.Join(b.homeDir, ".claude", "projects")

	registryIDs := b.signals.RegistrySessionIDs(ctx)
	liveIDsUnknown := !b.signals.LiveIDsKnown()
	resumeIDs := b.signals.ResumeSessionIDs(ctx)

	refreshed := make([]taskSnapshotRow, len(rows))
	copy(refreshed, rows)
	for i := range refreshed {
		item := refreshed[i].item
		refreshed[i].started = b.sessionStarted(vault.Name, item.Name)
		refreshed[i].sessionState = sessionStatePtr(session.ClassifySessionState(
			ctx,
			session.ClassifyParams{
				SessionID:          item.ClaudeSessionID,
				ProjectDir:         projectDir,
				ProjectsRoot:       projectsRoot,
				Now:                b.clock.Now().UTC(),
				LiveWindow:         session.DefaultLiveWindow,
				ResumeSessionIDs:   resumeIDs,
				RegistrySessionIDs: registryIDs,
				LiveIDsUnknown:     liveIDsUnknown,
				TranscriptMtime:    b.transcriptProbe(),
			},
		))
		refreshed[i].activityDate = activity.ComputeActivityDateWith(
			ctx,
			b.transcriptProbe(),
			parseDateTime(item.ModifiedDate),
			item.ClaudeSessionID,
			projectDir,
			projectsRoot,
		)
	}
	return refreshed, nil
}

// patchTaskRows re-derives only the rows whose pages the page index reports as
// changed, so a per-file write mark costs a bounded set of row derivations
// instead of a vault-wide rebuild.
//
// names are the changed pages' base filenames including their ".md" suffix. The
// pages come from the page index, whose read resolves any pending write mark, so
// a row is derived from the page the write produced rather than the one the
// snapshot held before it.
//
// Beyond the changed page's own row, two kinds of field need care:
//
//   - The classified session state and the activity date are carried over from
//     the held row. They come from the session snapshot, and the store only
//     takes this path when the session generation has not moved, so those values
//     are already the current generation's; recomputing them would need the
//     process spawn and transcript probe this path exists to avoid.
//   - The session-started marker is NOT carried over. It derives from the launch
//     registry and the status cache, both in memory and both moved by the write
//     that marked the page — Cache.Invalidate refreshes the cache and the launch
//     registry updates on Begin/Finish — so it is recomputed here at no I/O.
//     Carrying it over would republish a marker a ClearTaskSession write had
//     just cleared, for up to a session refresh.
//
// blockers and blocked are cross-row derived — uncompletedBlockers filters a
// row's own BlockedBy through the status cache, so one row's badge depends on
// another row's status — and are handled by recomputeDependentBlockers once the
// changed rows are spliced in.
//
// It returns a new slice. The rows it is given are shared with every reader, so
// each is copied before a field is written, and a row that is replaced is
// replaced whole rather than mutated.
//
// Anything this path cannot derive without data it does not already hold — a
// page with no held row, whose session-derived fields would need a process spawn
// and a transcript probe to recompute — falls back to the full build for the
// whole key rather than fetching it.
func (b *board) patchTaskRows(
	ctx context.Context,
	vault Vault,
	rows []taskSnapshotRow,
	names []string,
) ([]taskSnapshotRow, error) {
	pages, err := b.index.ListPages(ctx, vault.Path, vault.TasksFolder)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "list task pages for vault %s", vault.Name)
	}

	byName := make(map[string]*domain.Page, len(pages))
	for _, page := range pages {
		byName[page.FileMetadata.Name+".md"] = page
	}
	held := make(map[string]int, len(rows))
	for i, row := range rows {
		held[row.item.Name+".md"] = i
	}

	changed := make([]*domain.Page, 0, len(names))
	var dropped []int
	for _, name := range names {
		page, present := byName[name]
		at, wasHeld := held[name]
		switch {
		case present && wasHeld:
			changed = append(changed, page)
		case present:
			// A page with no held row carries no session data to reuse.
			return b.buildTaskRows(ctx, vault)
		case wasHeld:
			dropped = append(dropped, at)
		}
	}

	items, err := ops.NewListOperation(&changedPages{pages: changed}).Execute(
		ctx, vault.Path, vault.Name, vault.TasksFolder, nil, true, "", "",
	)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "derive changed task rows for vault %s", vault.Name)
	}
	itemByName := make(map[string]ops.TaskListItem, len(items))
	for _, item := range items {
		itemByName[item.Name] = item
	}

	patched := make([]taskSnapshotRow, len(rows))
	copy(patched, rows)
	for _, page := range changed {
		name := page.FileMetadata.Name
		item, ok := itemByName[name]
		if !ok {
			// The list operation dropped the page the snapshot holds.
			return b.buildTaskRows(ctx, vault)
		}
		at := held[name+".md"]
		blockers := b.uncompletedBlockers(vault.Name, item.BlockedBy)
		questions, questionsErr := pageOpenQuestions(ctx, page)
		if questionsErr != nil {
			glog.Warningf(
				"parse open questions of %s in vault %s: %v",
				name,
				vault.Name,
				questionsErr,
			)
			questions = []domain.OpenQuestion{}
		}
		patched[at] = taskSnapshotRow{
			item:     item,
			vault:    vault,
			blockers: blockers,
			blocked:  len(blockers) > 0,
			// The marker is recomputed, not carried over: it reads the launch
			// registry and the status cache, both in memory and both moved by the
			// write that marked this page, so it is free and it is current.
			started:       b.sessionStarted(vault.Name, item.Name),
			sessionState:  rows[at].sessionState,
			activityDate:  rows[at].activityDate,
			openQuestions: questions,
		}
	}
	if len(dropped) > 0 {
		patched = withoutRows(patched, dropped)
	}
	b.recomputeDependentBlockers(vault.Name, patched, names)
	return patched, nil
}

// recomputeDependentBlockers re-derives blockers and blocked for every row that
// names one of the changed pages in its own BlockedBy, after the changed rows
// have been spliced into patched.
//
// blockers/blocked are cross-row derived: uncompletedBlockers filters a row's
// own BlockedBy through the status cache, so row B's badge depends on row A's
// status. Re-deriving only the changed page's own row would leave every
// dependent of a changed page publishing the blockers it had before the write —
// completing a blocking task would leave its dependents showing as blocked until
// the next full build, which is the regression this walk closes.
//
// It keeps the patch's I/O bounded by the changed set: it reads only the
// BlockedBy lists the held rows already carry and re-runs the same in-memory
// cache lookups uncompletedBlockers already makes, so it adds no vault list, no
// page scan and no process spawn. The alternative — falling back to the full
// build whenever any held row depends on a changed page — would give the patch
// up entirely for the common case of a single blocked task, which is the cost
// this path exists to remove.
//
// names are the changed pages' base filenames including their ".md" suffix; a
// row whose own page is one of them was rebuilt by the caller and is skipped.
// Rows are copied by the caller before this runs, so a field write here never
// reaches a row a concurrent reader is holding.
func (b *board) recomputeDependentBlockers(
	vaultName string,
	rows []taskSnapshotRow,
	names []string,
) {
	if len(names) == 0 {
		return
	}
	changed := make(map[string]bool, len(names))
	for _, name := range names {
		changed[name] = true
	}
	for i := range rows {
		if changed[rows[i].item.Name+".md"] {
			// The row's own page changed, so its blockers were re-derived above.
			continue
		}
		if !blockedByNamesChangedPage(rows[i].item.BlockedBy, changed) {
			continue
		}
		blockers := b.uncompletedBlockers(vaultName, rows[i].item.BlockedBy)
		rows[i].blockers = blockers
		rows[i].blocked = len(blockers) > 0
	}
}

// blockedByNamesChangedPage reports whether any of a row's own BlockedBy
// wikilinks names a changed page. The comparison strips the wikilink brackets
// exactly as uncompletedBlockers does, so a hit is precisely the entry whose
// cached status the blocker read consults.
func blockedByNamesChangedPage(blockedBy []string, changed map[string]bool) bool {
	for _, wikilink := range blockedBy {
		if changed[stripBrackets(wikilink)+".md"] {
			return true
		}
	}
	return false
}

// withoutRows returns rows with the given positions removed, in a new slice.
func withoutRows(rows []taskSnapshotRow, dropped []int) []taskSnapshotRow {
	removed := make(map[int]bool, len(dropped))
	for _, at := range dropped {
		removed[at] = true
	}
	kept := make([]taskSnapshotRow, 0, len(rows)-len(dropped))
	for i, row := range rows {
		if removed[i] {
			continue
		}
		kept = append(kept, row)
	}
	return kept
}

// changedPages is the storage.PageStorage the row patch runs vault-cli's list
// operation over. It serves only the pages whose files changed, so the item
// construction, the filtering and the sorting stay vault-cli's and stay bounded
// to the changed set: the patch never lists the folder and never walks the
// vault.
type changedPages struct {
	pages []*domain.Page
}

// ListPages returns the changed pages.
func (c *changedPages) ListPages(
	_ context.Context, _, _ string,
) ([]*domain.Page, error) {
	return c.pages, nil
}

// ReadPage returns the changed page with the given bare base name. The list
// operation never calls it, so a miss is a programming error rather than a
// reachable state.
func (c *changedPages) ReadPage(
	ctx context.Context, _, _, name string,
) (*domain.Page, error) {
	for _, page := range c.pages {
		if page.FileMetadata.Name == name {
			return page, nil
		}
	}
	return nil, errors.Errorf(ctx, "page %q is not in the changed set", name)
}

// pageOpenQuestions returns one page's Open Questions in the wire shape the
// board exposes, never nil.
func pageOpenQuestions(ctx context.Context, page *domain.Page) ([]domain.OpenQuestion, error) {
	items, err := storage.ParseOpenQuestions(ctx, page.Content.String())
	if err != nil {
		return nil, err
	}
	// Marker and Line are parse bookkeeping for a rewriter, so only the
	// question and its position go on the wire. An already-answered item's
	// Answer is deliberately not exposed, and answered items are included
	// rather than filtered out: the section is the source of truth for what
	// the task is waiting on, and a UI is better placed to decide what an
	// already-answered item means.
	questions := make([]domain.OpenQuestion, 0, len(items))
	for _, item := range items {
		questions = append(questions, domain.OpenQuestion{
			Index: item.Index,
			Text:  item.Question,
		})
	}
	return questions, nil
}

// openQuestionsByTask returns each task page's Open Questions, keyed by the
// page's file name. It is one ListPages per vault — never one per task, which
// would put a vault walk on the board's hottest path. That listing is the same
// snapshot opsProvider.List already takes (pkg/factory/api.go builds it as
// ops.NewListOperation(p.pageIndex)), so it costs no extra I/O: the board is
// not reading pages for the first time, it is reading the snapshot a second
// time for a field the list rows do not carry.
//
// A failure is not fatal to a board read: it is logged and every task carries
// an empty list rather than the whole task list failing.
func (b *board) openQuestionsByTask(
	ctx context.Context, vault Vault,
) map[string][]domain.OpenQuestion {
	byName := map[string][]domain.OpenQuestion{}
	if b.pageIndex == nil {
		// A board built without a page index carries no open questions, on the
		// same terms as any other failure here: degrade, never fail the read.
		return byName
	}
	pages, err := b.pageIndex.ListPages(ctx, vault.Path, vault.TasksFolder)
	if err != nil {
		glog.Warningf("list pages for open questions in vault %s: %v", vault.Name, err)
		return byName
	}
	for _, page := range pages {
		select {
		case <-ctx.Done():
			// The caller is gone: stop rather than warn once per remaining page.
			return byName
		default:
		}
		questions, parseErr := pageOpenQuestions(ctx, page)
		if parseErr != nil {
			glog.Warningf(
				"parse open questions of %s in vault %s: %v",
				page.FileMetadata.Name,
				vault.Name,
				parseErr,
			)
			continue
		}
		byName[page.FileMetadata.Name] = questions
	}
	return byName
}

// openQuestionsFor returns the task's open questions, never nil, so the field
// serialises as [] rather than null.
func openQuestionsFor(
	byName map[string][]domain.OpenQuestion, name string,
) []domain.OpenQuestion {
	if questions, ok := byName[name]; ok && questions != nil {
		return questions
	}
	return []domain.OpenQuestion{}
}

// visibleRow applies the completed/deferred visibility rules, returning the row
// with upcoming/recently_completed set.
func (b *board) visibleRow(
	ctx context.Context,
	item ops.TaskListItem,
	now, cutoff, lookback time.Time,
) (taskRow, bool, error) {
	row := taskRow{item: item}
	if item.Status == "completed" {
		completedAt := parseFlexibleDate(item.CompletedDate)
		if completedAt == nil {
			if modifiedAt := parseDateTime(item.ModifiedDate); modifiedAt != nil {
				value := modifiedAt.Time()
				completedAt = &value
			}
		}
		if completedAt != nil && !completedAt.Before(lookback) {
			row.recentlyCompleted = true
			done := "done"
			row.phaseOverride = &done
			return row, true, nil
		}
		return row, false, nil
	}
	if item.DeferDate == "" {
		return row, true, nil
	}
	deferAt, ok := parseDeferDate(item.DeferDate)
	if !ok {
		return row, false, errors.Errorf(ctx, "invalid defer_date %q", item.DeferDate)
	}
	if !deferAt.After(now) {
		return row, true, nil
	}
	if !deferAt.After(cutoff) {
		row.upcoming = true
		return row, true, nil
	}
	return row, false, nil
}

func (b *board) uncompletedBlockers(vaultName string, blockedBy []string) []string {
	uncompleted := make([]string, 0, len(blockedBy))
	for _, wikilink := range blockedBy {
		name := stripBrackets(wikilink)
		status, _ := b.cache.GetStatus(vaultName, name)
		if status != "completed" {
			uncompleted = append(uncompleted, name)
		}
	}
	return uncompleted
}

func (b *board) sessionStarted(vaultName, itemID string) *string {
	if state, ok := b.launch.State(vaultName, itemID); ok && state == launchregistry.Finished {
		return nil
	}
	if started, ok := b.cache.GetSessionStarted(vaultName, itemID); ok {
		return &started
	}
	return nil
}

// taskResponse renders one visible row. activityDate is the precomputed
// transcript-aware activity date, so the request path never probes.
func (b *board) taskResponse(
	vault Vault,
	row taskRow,
	activityDate *libtime.DateTime,
) api.TaskResponse {
	item := row.item

	phase := strPtr(item.Phase)
	if row.phaseOverride != nil {
		phase = row.phaseOverride
	}

	blockers := row.blockers
	if blockers == nil {
		blockers = []string{}
	}

	openQuestions := row.openQuestions
	if openQuestions == nil {
		openQuestions = []domain.OpenQuestion{}
	}

	var sessionID *string
	if item.ClaudeSessionID != "" {
		sessionID = &item.ClaudeSessionID
	}

	return api.TaskResponse{
		ID:                   item.Name,
		Title:                item.Name,
		Status:               item.Status,
		Phase:                phase,
		ProjectPath:          nil,
		Description:          nil,
		ModifiedDate:         dateTimeString(parseDateTime(item.ModifiedDate)),
		CompletedDate:        strPtr(item.CompletedDate),
		ObsidianURL:          obsidianURL(vault.VaultName, vault.TasksFolder+"/"+item.Name+".md"),
		DeferDate:            strPtr(item.DeferDate),
		PlannedDate:          strPtr(item.PlannedDate),
		DueDate:              strPtr(item.DueDate),
		Priority:             priorityValue(item.Priority),
		Category:             strPtr(item.Category),
		Recurring:            strPtr(item.Recurring),
		ClaudeSessionID:      sessionID,
		ClaudeSessionStarted: row.started,
		Assignee:             strPtr(item.Assignee),
		BlockedBy:            item.BlockedBy,
		Blocked:              row.blocked,
		Blockers:             blockers,
		Upcoming:             row.upcoming,
		RecentlyCompleted:    row.recentlyCompleted,
		Vault:                vault.Name,
		Goals:                goalsValue(item.Goals),
		Flag:                 item.Flag,
		ActivityDate:         dateTimeString(activityDate),
		SessionState:         row.sessionState,
		OpenQuestions:        openQuestions,
	}
}

// phaseMatches reproduces the Python phase filter: a task at a valid phase must
// be in the filter; a task at an unrecognized phase matches when "todo" is in
// the filter.
func phaseMatches(phase string, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	if hasString(validPhases, phase) {
		return hasString(filter, phase)
	}
	return hasString(filter, "todo")
}

// assigneeMatches reproduces the Python assignee filter: an empty token matches
// unassigned work, a non-empty token matches exactly.
func assigneeMatches(assignee string, filter []string) bool {
	if filter == nil {
		return true
	}
	for _, token := range filter {
		if token == "" && assignee == "" {
			return true
		}
		if token != "" && assignee == token {
			return true
		}
	}
	return false
}

// goalMatches reproduces the Python goal filter.
func goalMatches(goals []string, filter []string) bool {
	if filter == nil {
		return true
	}
	for _, goal := range goals {
		if hasString(filter, goal) {
			return true
		}
	}
	return false
}

// goalsValue strips wikilink brackets from goal names, returning nil when the
// list is empty (Python emits null).
func goalsValue(goals []string) []string {
	if len(goals) == 0 {
		return nil
	}
	stripped := make([]string, 0, len(goals))
	for _, goal := range goals {
		stripped = append(stripped, stripWikilink(goal))
	}
	if len(stripped) == 0 {
		return nil
	}
	return stripped
}

// priorityValue reproduces the Python priority read: 0 is absent (null), any
// non-zero value is a number.
func priorityValue(priority int) any {
	if priority == 0 {
		return nil
	}
	return priority
}

// dateTimeString renders a nullable libtime.DateTime as a JSON string the way
// the Python backend does: UTC with a literal Z suffix, exactly six fractional
// digits when the microsecond component is non-zero, and no fractional part at
// all when it is zero. The sub-microsecond remainder is truncated, not rounded.
func dateTimeString(value *libtime.DateTime) *string {
	if value == nil {
		return nil
	}
	truncated := value.UTC().Time().Truncate(time.Microsecond)
	var formatted string
	if truncated.Nanosecond() == 0 {
		formatted = truncated.Format(time.RFC3339)
	} else {
		formatted = truncated.Format("2006-01-02T15:04:05.000000Z07:00")
	}
	return &formatted
}

// parseFlexibleDate parses a date-only or RFC3339 value as UTC midnight/instant.
func parseFlexibleDate(value string) *time.Time {
	if value == "" {
		return nil
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		utc := parsed.UTC()
		return &utc
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		utc := parsed.UTC()
		return &utc
	}
	return nil
}

// sessionStatePtr renders the classification as a nullable string: the "none"
// state (empty session id) is Python None.
func sessionStatePtr(state session.SessionState) *string {
	if state == session.SessionStateNone {
		return nil
	}
	value := string(state)
	return &value
}
