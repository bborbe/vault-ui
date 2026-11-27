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

	"github.com/bborbe/vault-ui/pkg/activity"
	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/cleanup"
	"github.com/bborbe/vault-ui/pkg/session"
	"github.com/bborbe/vault-ui/pkg/vaultapi"
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
	location          *string
	jumpPane          *string
	jira              *string
	mode              *string
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
			visibleTask.location = row.location
			visibleTask.jumpPane = row.jumpPane
			visibleTask.jira = row.jira
			visibleTask.mode = row.mode
			visibleTask.openQuestions = row.openQuestions
			response := b.taskResponse(vault, visibleTask, row.activityDate)
			response.HoldReason = b.holdFor(vault, holdKindTask, row.item.Name)
			responses = append(responses, response)
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
// cache, which builds it once per (selected-vault signal vector, query) and
// holds the identity and gzip forms. A hit is served the stored bytes with no
// projection and no marshal.
//
// It reads the snapshot for the query's vaults before consulting the cache. The
// selected vaults' published-rows signals the cache keys on only move when a
// read rebuilds a key with rows that differ from the ones it last published,
// and a cache hit would never make that read: an external page edit or a new
// session snapshot would otherwise leave a held body serving indefinitely. The
// read is a per-vault revision check that returns the published rows unchanged
// when nothing moved, so it rebuilds only what the page index has marked — the
// same read ListTasks makes.
//
// The read drives each selected vault and drops its rows. It does not
// concatenate them: the only reader that wants the rows is the cache's clock
// boundary, which reads them through its own seam on a miss. Concatenating them
// here allocated and copied every row of every selected vault on every request,
// hit or miss, and then discarded the result — the whole of a warm request's
// allocation.
//
// The selection is loaded once and handed to the cache, so the cache's signals
// are read for exactly the vaults whose rows were read, and the signals seam
// itself performs no vault I/O.
func (b *board) ListTasksBody(ctx context.Context, query TaskQuery) (TaskListBody, error) {
	all, err := b.vaults.Vaults(ctx)
	if err != nil {
		return TaskListBody{}, errors.Wrap(ctx, err, "list vaults")
	}
	selected := b.selectVaults(all, query.Vaults)
	if err := b.refreshTaskSnapshots(ctx, selected); err != nil {
		return TaskListBody{}, err
	}
	return b.bodies.Get(ctx, query, selected)
}

// refreshTaskSnapshots drives each selected vault's published-rows read and
// drops the rows. A read is what publishes a vault's rows and advances its
// signal, so a cache hit must still make it or a held body would never be
// invalidated; the rows themselves are not wanted here.
//
// It allocates nothing: snapshot.List returns the published slice, which the
// store shares with every reader and which is discarded here rather than copied.
func (b *board) refreshTaskSnapshots(ctx context.Context, selected []Vault) error {
	for _, vault := range selected {
		if _, err := b.snapshot.List(ctx, vault); err != nil {
			return err
		}
	}
	return nil
}

// taskSnapshotSignals returns each selected vault's published-rows signal, in
// selection order. A vault the query does not select is absent, so a change in
// it cannot invalidate the query's held body; the bare query selects every
// configured vault, so a genuine change in any of them does. It reads the same
// selection ListTasks makes and performs no vault I/O.
func (b *board) taskSnapshotSignals(selected []Vault) []VaultSignal {
	signals := make([]VaultSignal, 0, len(selected)+1)
	for _, vault := range selected {
		signals = append(signals, VaultSignal{
			Name:   vault.Name,
			Signal: b.snapshot.Signal(vaultapi.NewSnapshotKey(vault.Path, vault.TasksFolder)),
		})
	}
	// The wrapper's held set is not part of any vault's rows, so a change in it
	// must move the signal vector on its own or a held body would keep serving
	// the previous hold reasons.
	if b.holds != nil {
		signals = append(signals, VaultSignal{Name: holdsSignalName, Signal: b.holds.Generation()})
	}
	return signals
}

// taskSnapshotRows returns the snapshot's rows for the query's selected vaults,
// one slice per selected vault in selection order, before the status filter and
// visibleRow drop any. The body cache reads it to derive its clock boundary, so
// a row the visibility filter drops still contributes its entry instant.
//
// It returns the per-vault published slices rather than one concatenated slice:
// the store shares each slice with every reader, so returning them as they are
// copies nothing. Concatenating them allocated and copied every row of every
// selected vault on every body-cache miss — the single largest allocation on the
// board's request path — only for the caller to walk them once and discard them.
//
// It makes the same vault selection ListTasks makes and reads the same
// snapshot, so it adds no vault I/O: the snapshot List is served from the
// published rows.
func (b *board) taskSnapshotRows(
	ctx context.Context,
	query TaskQuery,
) ([][]taskSnapshotRow, error) {
	all, err := b.vaults.Vaults(ctx)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "list vaults")
	}
	selected := b.selectVaults(all, query.Vaults)

	perVault := make([][]taskSnapshotRow, 0, len(selected))
	for _, vault := range selected {
		vaultRows, snapshotErr := b.snapshot.List(ctx, vault)
		if snapshotErr != nil {
			return nil, snapshotErr
		}
		perVault = append(perVault, vaultRows)
	}
	return perVault, nil
}

// buildTaskRows lists the vault's tasks and precomputes every field that needs
// I/O: the uncompleted blockers, the blocked flag, the session-started marker,
// the classified session state, the session's location and the activity date. It
// applies none of the request-time filters, so one build serves every query.
func (b *board) buildTaskRows(ctx context.Context, vault Vault) ([]taskSnapshotRow, error) {
	items, err := b.ops.List(vault).Execute(
		ctx, vault.Path, vault.Name, vault.TasksFolder, nil, true, "", "",
	)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "list tasks for vault %s", vault.Name)
	}

	sig := b.taskRowSignals(ctx, vault)

	// One ListPages for the whole vault, attached to the rows so the request
	// path never reads a page — the same precompute the other fields get.
	pageFields := b.taskPageFields(vault)
	statuses := b.taskStatuses(vault.VaultName)

	rows := make([]taskSnapshotRow, 0, len(items))
	for _, item := range items {
		rows = append(
			rows,
			b.deriveTaskRow(ctx, vault, item, taskPageFieldsFor(pageFields, item.Name), statuses, sig),
		)
	}
	b.attachJumpPanes(ctx, rows)
	return rows, nil
}

// taskRowSignals is the per-vault session precompute the per-row derivation
// classifies against. It is read once per build (or per patch), never per row:
// the session id sets and the transcript probe are the same for every row of a
// vault, so reading them inside the loop would repeat the work for no reason.
type taskRowSignals struct {
	projectDir   string
	projectsRoot string
	registryIDs  []string
	seenIDs      []string
	resumeIDs    []string
	liveUnknown  bool
}

// taskRowSignals reads the vault's session precompute.
func (b *board) taskRowSignals(ctx context.Context, vault Vault) taskRowSignals {
	return taskRowSignals{
		projectDir:   cleanup.DeriveClaudeProjectDir(b.homeDir, vault.Path, vault.SessionProjectDir),
		projectsRoot: filepath.Join(b.homeDir, ".claude", "projects"),
		registryIDs:  b.signals.RegistrySessionIDs(ctx),
		seenIDs:      b.signals.SeenSessionIDs(ctx),
		resumeIDs:    b.signals.ResumeSessionIDs(ctx),
		liveUnknown:  !b.signals.LiveIDsKnown(),
	}
}

// deriveTaskRow builds one snapshot row from a list item and the page-derived
// fields, classifying the session-dependent fields against sig. It is the single
// per-row derivation both the full build and the patch path's new-page splice
// use, so a spliced row carries exactly the fields a full rebuild would produce
// for the same item and page.
func (b *board) deriveTaskRow(
	ctx context.Context,
	vault Vault,
	item ops.TaskListItem,
	fields taskPageFields,
	statuses map[string]string,
	sig taskRowSignals,
) taskSnapshotRow {
	blockers := b.uncompletedBlockers(statuses, item.BlockedBy)
	state := session.ClassifySessionState(ctx, session.ClassifyParams{
		SessionID:          item.ClaudeSessionID,
		ProjectDir:         sig.projectDir,
		ProjectsRoot:       sig.projectsRoot,
		Now:                b.clock.Now().UTC(),
		LiveWindow:         session.DefaultLiveWindow,
		ResumeSessionIDs:   sig.resumeIDs,
		RegistrySessionIDs: sig.registryIDs,
		SeenSessionIDs:     sig.seenIDs,
		LiveIDsUnknown:     sig.liveUnknown,
		TranscriptMtime:    b.transcriptProbe(),
	})
	return taskSnapshotRow{
		item:         item,
		vault:        vault,
		blockers:     blockers,
		blocked:      len(blockers) > 0,
		started:      b.sessionStarted(vault.Name, item.Name),
		sessionState: sessionStatePtr(state),
		location: liveLocationPtr(
			state, b.signals.RegistryLocation(ctx, item.ClaudeSessionID),
		),
		activityDate: activity.ComputeActivityDateWith(
			ctx,
			b.transcriptProbe(),
			parseDateTime(item.ModifiedDate),
			item.ClaudeSessionID,
			sig.projectDir,
			sig.projectsRoot,
		),
		jira:          fields.jira,
		mode:          fields.mode,
		openQuestions: fields.openQuestions,
	}
}

// attachJumpPanes resolves the panes of the live rows in one batch and stores
// each live row's pane on the row. A nil Panes dependency leaves every row's
// jumpPane nil, which is the feature-inert configuration. Only live rows are
// resolved: a headless worker is live but has no terminal.
func (b *board) attachJumpPanes(ctx context.Context, rows []taskSnapshotRow) {
	if b.panes == nil {
		return
	}
	seen := make(map[string]bool, len(rows))
	sessionIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		sessionID := row.item.ClaudeSessionID
		if !isLiveRow(row) || sessionID == "" || seen[sessionID] {
			continue
		}
		seen[sessionID] = true
		sessionIDs = append(sessionIDs, sessionID)
	}
	if len(sessionIDs) == 0 {
		return
	}
	panes := b.panes.ResolveAll(ctx, sessionIDs)
	for i := range rows {
		if !isLiveRow(rows[i]) {
			continue
		}
		if paneID, ok := panes[rows[i].item.ClaudeSessionID]; ok {
			value := paneID
			rows[i].jumpPane = &value
		}
	}
}

// isLiveRow reports whether the row's classified session state is live.
func isLiveRow(row taskSnapshotRow) bool {
	return row.sessionState != nil && *row.sessionState == string(session.SessionStateLive)
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
	seenIDs := b.signals.SeenSessionIDs(ctx)
	liveIDsUnknown := !b.signals.LiveIDsKnown()
	resumeIDs := b.signals.ResumeSessionIDs(ctx)

	refreshed := make([]taskSnapshotRow, len(rows))
	copy(refreshed, rows)
	seen := make(map[string]bool, len(rows))
	sessionIDs := make([]string, 0, len(rows))
	for i := range refreshed {
		item := refreshed[i].item
		refreshed[i].started = b.sessionStarted(vault.Name, item.Name)
		state := session.ClassifySessionState(
			ctx,
			session.ClassifyParams{
				SessionID:          item.ClaudeSessionID,
				ProjectDir:         projectDir,
				ProjectsRoot:       projectsRoot,
				Now:                b.clock.Now().UTC(),
				LiveWindow:         session.DefaultLiveWindow,
				ResumeSessionIDs:   resumeIDs,
				RegistrySessionIDs: registryIDs,
				SeenSessionIDs:     seenIDs,
				LiveIDsUnknown:     liveIDsUnknown,
				TranscriptMtime:    b.transcriptProbe(),
			},
		)
		refreshed[i].sessionState = sessionStatePtr(state)
		refreshed[i].location = liveLocationPtr(
			state, b.signals.RegistryLocation(ctx, item.ClaudeSessionID),
		)
		// The row was copied, so a row that leaves live keeps the pointer it was
		// copied with unless it is cleared here. Clear it first, then collect the
		// ids to resolve in one batch after the pass.
		refreshed[i].jumpPane = nil
		if isLiveRow(refreshed[i]) && item.ClaudeSessionID != "" && !seen[item.ClaudeSessionID] {
			seen[item.ClaudeSessionID] = true
			sessionIDs = append(sessionIDs, item.ClaudeSessionID)
		}
		refreshed[i].activityDate = activity.ComputeActivityDateWith(
			ctx,
			b.transcriptProbe(),
			parseDateTime(item.ModifiedDate),
			item.ClaudeSessionID,
			projectDir,
			projectsRoot,
		)
	}
	if b.panes != nil && len(sessionIDs) > 0 {
		panes := b.panes.ResolveAll(ctx, sessionIDs)
		for i := range refreshed {
			if !isLiveRow(refreshed[i]) {
				continue
			}
			if paneID, ok := panes[refreshed[i].item.ClaudeSessionID]; ok {
				value := paneID
				refreshed[i].jumpPane = &value
			}
		}
	}
	return refreshed, nil
}

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
		blockers := b.uncompletedBlockers(b.taskStatuses(vaultName), rows[i].item.BlockedBy)
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

// taskPageFields is the per-task state the board derives from a task page that
// the list rows do not carry: the Open Questions section and the page's own
// `jira:` frontmatter value.
type taskPageFields struct {
	openQuestions []domain.OpenQuestion
	jira          *string
	mode          *string
}

// taskPageFields returns the per-task Open Questions and jira/mode values from
// the vault-api source, which carries them on the task item.
func (b *board) taskPageFields(vault Vault) map[string]taskPageFields {
	return b.taskPageFieldsFromSource(vault)
}

// taskPageFieldsFromSource returns each task's Open Questions and `jira:`/`mode:`
// values from the vault-api source, keyed by the page name. The vault-api task
// item carries them directly, so the board no longer reads pages for them — this
// is what lets the page index go.
func (b *board) taskPageFieldsFromSource(vault Vault) map[string]taskPageFields {
	byName := map[string]taskPageFields{}
	if b.source == nil {
		return byName
	}
	for _, t := range b.source.Tasks(vault.VaultName) {
		byName[t.Title] = taskPageFields{
			openQuestions: convertOpenQuestions(t.OpenQuestions),
			jira:          t.Jira,
			mode:          t.Mode,
		}
	}
	return byName
}

// convertOpenQuestions maps vault-api's Open Questions onto vault-cli's domain
// type, which the snapshot row and the response carry.
func convertOpenQuestions(in []vaultapi.OpenQuestion) []domain.OpenQuestion {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.OpenQuestion, 0, len(in))
	for _, q := range in {
		out = append(out, domain.OpenQuestion{Index: q.Index, Text: q.Text})
	}
	return out
}

// taskPageFieldsFor returns the task's page-derived fields. The Open Questions
// are never nil, so the field serialises as [] rather than null; a page absent
// from the listing carries a nil Jira, which serialises as null.
func taskPageFieldsFor(byName map[string]taskPageFields, name string) taskPageFields {
	fields := byName[name]
	if fields.openQuestions == nil {
		fields.openQuestions = []domain.OpenQuestion{}
	}
	return fields
}

// pageJira returns a page's `jira:` frontmatter value as a nullable string,
// verbatim and unparsed: in the vaults it is as often a full browse URL as a
// bare key, and the frontend finds the key in either. It is nil when the page
// carries no value, the same nil-for-empty shape the other pointer fields use.
func pageJira(page *domain.Page) *string {
	value := page.GetString("jira")
	if value == "" {
		return nil
	}
	return &value
}

// pageMode returns a page's `mode:` frontmatter value as a nullable string,
// verbatim and unparsed: in the vaults the field is free-form, and an empty
// value reads as absent — the same nil-for-empty shape pageJira uses.
func pageMode(page *domain.Page) *string {
	value := page.GetString("mode")
	if value == "" {
		return nil
	}
	return &value
}

// pageClaudeSessionStarted returns a page's `claude_session_started` marker as a
// nullable string, on the same nil-for-empty terms as pageJira and pageMode.
//
// A topic card needs this from the page rather than from the status cache: the
// cache indexes Themes/Objectives/Goals/Tasks only, so a topic's marker is never
// in it and the card would never render "Starting…" across a reload.
func pageClaudeSessionStarted(page *domain.Page) *string {
	value := page.GetString("claude_session_started")
	if value == "" {
		return nil
	}
	return &value
}

// deferVisibility is the one deferred-visibility rule shared by the Tasks,
// Goals and Topics list paths: a row with no defer date is visible and not
// upcoming; a defer in the past is visible; a defer inside the upcoming window
// is visible and marked upcoming; a defer beyond the window is hidden.
func deferVisibility(
	ctx context.Context,
	deferDate string,
	now, cutoff time.Time,
) (visible, upcoming bool, err error) {
	if deferDate == "" {
		return true, false, nil
	}
	deferAt, ok := parseDeferDate(deferDate)
	if !ok {
		return false, false, errors.Errorf(ctx, "invalid defer_date %q", deferDate)
	}
	if !deferAt.After(now) {
		return true, false, nil
	}
	if !deferAt.After(cutoff) {
		return true, true, nil
	}
	return false, false, nil
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
	visible, upcoming, err := deferVisibility(ctx, item.DeferDate, now, cutoff)
	if err != nil {
		return row, false, err
	}
	row.upcoming = upcoming
	return row, visible, nil
}

// uncompletedBlockers returns the blockers whose status is not completed, using
// the statuses the board already holds from the vault-api source — no status
// cache.
func (b *board) uncompletedBlockers(statuses map[string]string, blockedBy []string) []string {
	uncompleted := make([]string, 0, len(blockedBy))
	for _, wikilink := range blockedBy {
		name := stripBrackets(wikilink)
		if statuses[name] != "completed" {
			uncompleted = append(uncompleted, name)
		}
	}
	return uncompleted
}

// sessionStarted is always nil: the started marker lived in the status cache,
// which the board no longer reads, and the vault-api task payload carries no
// started marker.
func (b *board) sessionStarted(_, _ string) *string { return nil }

// taskStatuses maps each task's page name to its status from the vault-api
// source, so blocker resolution needs no status cache.
func (b *board) taskStatuses(vaultName string) map[string]string {
	statuses := map[string]string{}
	if b.source == nil {
		return statuses
	}
	for _, t := range b.source.Tasks(vaultName) {
		statuses[t.Title] = t.Status
	}
	return statuses
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
		Location:             row.location,
		JumpPane:             row.jumpPane,
		Jira:                 row.jira,
		Mode:                 row.mode,
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

// liveLocationPtr renders the store's location for a live session as a nullable
// wire value: nil unless the session is live and the store reported a non-empty
// location. A live session the store gives no location for — and a session
// classified live by transcript recency or a live resume process rather than by
// the store — therefore carries null, and the badge renders the plain "● Live".
func liveLocationPtr(state session.SessionState, location string) *string {
	if state != session.SessionStateLive || location == "" {
		return nil
	}
	return &location
}
