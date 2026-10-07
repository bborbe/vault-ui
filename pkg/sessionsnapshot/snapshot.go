// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package sessionsnapshot holds the session-liveness inputs the board renders
// in memory: the live session-registry ids, the live `--resume`/`--session-id`
// ids, and a per-refresh cache of session transcript modification times.
//
// A read never spawns a process and never reads the registry directory. The
// `ps` scan runs once per refresh on a fixed interval of at least 60 s, and the
// transcript probe runs at most once per session per refresh window, so no
// request path pays for either.
//
// The classification contract itself (the four outcomes, the fixed signal
// order, the five-minute window) is unchanged and lives in pkg/session,
// documented in docs/liveness-classification.md. Only the source of the two
// inputs changes.
package sessionsnapshot

import (
	"context"
	"sync"
	"time"

	"github.com/bborbe/errors"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/activity"
	"github.com/bborbe/vault-ui/pkg/session"
)

// SessionRefreshInterval is how often the session snapshot re-reads the session
// inputs. It is deliberately at least 60 s: this is the only timer the spec
// adds, and the goal forbids a tight refresher.
const SessionRefreshInterval = 60 * time.Second

//counterfeiter:generate -o ./mocks/session-snapshot.go --fake-name Snapshot . Snapshot

// Snapshot is the process-wide, timer-refreshed view of the session-liveness
// inputs. A read never spawns a process and never reads the registry directory.
type Snapshot interface {
	// RegistrySessionIDs returns the live registry session ids from the last
	// successful refresh. Never nil.
	RegistrySessionIDs(ctx context.Context) []string
	// ResumeSessionIDs returns the live --resume/--session-id ids from the last
	// successful refresh. Never nil.
	ResumeSessionIDs(ctx context.Context) []string
	// TranscriptMtime returns the cached transcript mtime for the session,
	// probing at most once per session per refresh window. It returns nil for a
	// missing or blank session id and for a transcript that cannot be found,
	// exactly as activity.TranscriptMtime does.
	TranscriptMtime(ctx context.Context, sessionID, projectDir, projectsRoot string) *libtime.DateTime
	// Generation advances by one on every successful refresh; it is how a
	// consumer detects that a new session snapshot is available.
	Generation() uint64
	// RefreshOnce performs one refresh and returns the first error. A failed
	// refresh keeps the previous values.
	RefreshOnce(ctx context.Context) error
	// Run refreshes once, then once per Interval until ctx is done. It returns
	// nil on cancellation.
	Run(ctx context.Context) error
}

// Params carries the snapshot's injectable dependencies.
type Params struct {
	// Registry returns the live registry session ids. It never errors.
	Registry func(ctx context.Context) []string
	// Scanner produces fresh `ps` output; one scan runs per refresh.
	Scanner session.ProcessScanner
	// Probe returns a session transcript's mtime. Nil selects the snapshot's own
	// per-epoch listing of the projects root, which is the production path; a
	// non-nil probe takes precedence over that listing and exists as the test
	// seam. Production must leave it nil — injecting activity.TranscriptMtime
	// here restores the per-session filepath.Glob the index exists to remove.
	Probe activity.TranscriptMtimeGetter
	// Clock is the injected clock; never read the wall clock.
	Clock libtime.CurrentDateTimeGetter
	// Waiter is the injected sleeper, so a test drives the interval. There is
	// no interval knob: SessionRefreshInterval is the only period.
	Waiter libtime.WaiterDuration
}

// NewSnapshot creates an empty snapshot. Call RefreshOnce or Run before serving.
func NewSnapshot(params Params) Snapshot {
	return &snapshot{
		params:      params,
		registryIDs: []string{},
		resumeIDs:   []string{},
		transcripts: map[transcriptKey]transcriptEntry{},
		indices:     newTranscriptIndexSet(),
	}
}

// transcriptKey identifies one cached transcript verdict: the same session id
// under a different project directory or projects root is a different path.
type transcriptKey struct {
	sessionID    string
	projectDir   string
	projectsRoot string
}

// transcriptEntry is one cached transcript verdict with the instant it was
// probed, so a verdict is discarded once a later refresh has been published.
type transcriptEntry struct {
	probedAt libtime.DateTime
	mtime    *libtime.DateTime
}

type snapshot struct {
	params Params

	mu          sync.Mutex
	registryIDs []string
	resumeIDs   []string
	refreshedAt *libtime.DateTime
	generation  uint64
	transcripts map[transcriptKey]transcriptEntry
	// indices holds one listing of each projects root, built on first use and
	// dropped with the epoch. Resolving a session through it costs a map hit and
	// one stat, where activity.TranscriptMtime's filepath.Glob fallback re-reads
	// the whole projects root — and every directory under it — per call.
	indices *transcriptIndexSet
}

// transcriptIndexSet is one epoch's projects-root listings. It is held by
// pointer so a lookup that was building a listing when a refresh swapped the
// set can tell the set changed — a map cannot be compared, a pointer can.
type transcriptIndexSet struct {
	byRoot map[string]*activity.TranscriptIndex
}

// newTranscriptIndexSet returns an empty set, which is what a new epoch starts
// from.
func newTranscriptIndexSet() *transcriptIndexSet {
	return &transcriptIndexSet{byRoot: map[string]*activity.TranscriptIndex{}}
}

// RegistrySessionIDs returns a copy of the last successful refresh's registry
// ids. The result is never nil and never aliases the internal slice.
func (s *snapshot) RegistrySessionIDs(_ context.Context) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.registryIDs...)
}

// ResumeSessionIDs returns a copy of the last successful refresh's resume ids.
// The result is never nil and never aliases the internal slice.
func (s *snapshot) ResumeSessionIDs(_ context.Context) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.resumeIDs...)
}

// Generation returns the number of successful refreshes so far.
func (s *snapshot) Generation() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

// TranscriptMtime returns the transcript mtime for the session, served from the
// cache while the cached verdict is still inside the current refresh window.
//
// A nil result is cached too, so an absent transcript is probed once per
// window, not once per call. The mutex is never held across the probe: two
// concurrent first lookups for the same session in one window may both probe,
// which is harmless and bounded.
func (s *snapshot) TranscriptMtime(
	ctx context.Context,
	sessionID, projectDir, projectsRoot string,
) *libtime.DateTime {
	if sessionID == "" {
		return nil
	}

	key := transcriptKey{
		sessionID:    sessionID,
		projectDir:   projectDir,
		projectsRoot: projectsRoot,
	}

	s.mu.Lock()
	if entry, ok := s.transcripts[key]; ok &&
		s.refreshedAt != nil && !entry.probedAt.Before(*s.refreshedAt) {
		value := entry.mtime
		s.mu.Unlock()
		return value
	}
	s.mu.Unlock()

	var value *libtime.DateTime
	if probe := s.params.Probe; probe != nil {
		value = probe(ctx, sessionID, projectDir, projectsRoot)
	} else {
		value = s.indexedMtime(ctx, sessionID, projectDir, projectsRoot)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.transcripts[key] = transcriptEntry{probedAt: s.params.Clock.Now(), mtime: value}
	return value
}

// indexedMtime resolves a transcript through the epoch's listing of the projects
// root, building that listing on first use. It is the default probe, used when no
// probe is injected.
//
// The listing is built outside the mutex, so two concurrent first lookups for one
// root may both build it; both are equivalent and the second publish wins, which
// is the same bounded duplication TranscriptMtime's own comment accepts.
func (s *snapshot) indexedMtime(
	ctx context.Context,
	sessionID, projectDir, projectsRoot string,
) *libtime.DateTime {
	root := projectsRoot
	if root == "" {
		root = activity.DefaultProjectsRoot()
	}

	s.mu.Lock()
	set := s.indices
	index, ok := set.byRoot[root]
	s.mu.Unlock()
	if ok {
		return index.Mtime(ctx, sessionID, projectDir)
	}

	built, complete := activity.NewTranscriptIndex(ctx, root)

	s.mu.Lock()
	switch {
	case s.indices != set:
		// A refresh swapped the set while this listing was being built. That
		// listing predates the refresh, so publishing it would serve stale
		// verdicts for the whole new epoch — the very thing the reset exists to
		// prevent. Use whatever the new epoch has already built; when it has
		// nothing yet, this one call still answers from the pre-refresh listing
		// rather than rebuilding, which is bounded to a single lookup.
		if existing := s.indices.byRoot[root]; existing != nil {
			built = existing
		}
	case !complete:
		// The listing was abandoned part-way. Serve it for this call, but do
		// not publish it: a truncated listing reads every session it did not
		// reach as absent, and the snapshot is process-wide, so caching one
		// would misreport those sessions for every caller until the next
		// refresh. A single cancelled request is enough to cause it — /api/goals
		// passes a cancellable per-request context straight down to the probe,
		// unlike /api/tasks, whose build detaches cancellation.
	default:
		// Complete, and still the same epoch: publish it for the rest of the
		// epoch's lookups, which is what makes them a map hit.
		set.byRoot[root] = built
	}
	s.mu.Unlock()

	return built.Mtime(ctx, sessionID, projectDir)
}

// RefreshOnce runs one `ps` scan and one registry read and swaps both in
// atomically, advancing Generation. A scan failure keeps the previous values
// and returns the wrapped error without swapping.
func (s *snapshot) RefreshOnce(ctx context.Context) error {
	output, err := s.params.Scanner(ctx)
	if err != nil {
		glog.Errorf(
			"[SessionSnapshot] refresh failed (interval %v): %v",
			SessionRefreshInterval,
			err,
		)
		return errors.Wrap(ctx, err, "refresh session snapshot")
	}
	resumeIDs := session.ParseLiveSessionIDs(output)
	registryIDs := s.params.Registry(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.registryIDs = append([]string{}, registryIDs...)
	s.resumeIDs = append([]string{}, resumeIDs...)
	s.refreshedAt = s.params.Clock.Now().Ptr()
	// The projects-root listings belong to the epoch that just ended: a
	// transcript that appeared or vanished must be seen, not served from a
	// listing taken before the refresh.
	s.indices = newTranscriptIndexSet()
	s.generation++
	return nil
}
