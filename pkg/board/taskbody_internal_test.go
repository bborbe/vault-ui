// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board

import (
	"bytes"
	"compress/gzip"
	"context"
	stderrors "errors"
	"io"
	"strings"
	"sync"
	"time"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/ops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/pageindex"
)

// bodyBaseTime is the fixed request time the body tests build their fixtures
// around.
var bodyBaseTime = time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)

// fakeBodyClock is a settable CurrentDateTimeGetter, so a test can advance the
// request time across a computed boundary.
type fakeBodyClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeBodyClock(now time.Time) *fakeBodyClock {
	return &fakeBodyClock{now: now}
}

// Now returns the clock's current time.
func (c *fakeBodyClock) Now() libtime.DateTime {
	c.mu.Lock()
	defer c.mu.Unlock()
	return libtime.DateTime(c.now)
}

// set advances the clock.
func (c *fakeBodyClock) set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// bodyHarness wires a taskBodyCache to counting seams: the projection and encode
// counters prove a hit performs neither, the generation is settable so a test
// can move it, and the clock and rows are settable so a test can exercise the
// boundary.
type bodyHarness struct {
	cache *taskBodyCache
	clock *fakeBodyClock

	mu          sync.Mutex
	projections int
	encodes     int
	generation  uint64

	rowsFn func(ctx context.Context, query TaskQuery) ([]taskSnapshotRow, error)
}

func newBodyHarness(
	project func(ctx context.Context, query TaskQuery) ([]api.TaskResponse, error),
	rowsFns ...func(ctx context.Context, query TaskQuery) ([]taskSnapshotRow, error),
) *bodyHarness {
	h := &bodyHarness{clock: newFakeBodyClock(bodyBaseTime)}
	h.rowsFn = func(_ context.Context, _ TaskQuery) ([]taskSnapshotRow, error) {
		return nil, nil
	}
	if len(rowsFns) > 0 && rowsFns[0] != nil {
		h.rowsFn = rowsFns[0]
	}
	h.cache = newTaskBodyCache(taskBodyParams{
		Project: func(ctx context.Context, query TaskQuery) ([]api.TaskResponse, error) {
			h.mu.Lock()
			h.projections++
			h.mu.Unlock()
			return project(ctx, query)
		},
		Encode: func(value any) ([]byte, error) {
			h.mu.Lock()
			h.encodes++
			h.mu.Unlock()
			return encodeTaskList(value)
		},
		Generations: h.Generation,
		Clock:       h.clock,
		Rows: func(ctx context.Context, query TaskQuery) ([]taskSnapshotRow, error) {
			return h.rowsFn(ctx, query)
		},
	})
	return h
}

// bodySnapshotRow builds one pre-filter snapshot row, the shape
// taskBodyBoundary reads.
func bodySnapshotRow(name string, mutate ...func(*ops.TaskListItem)) taskSnapshotRow {
	entry := ops.TaskListItem{Name: name, Status: "todo"}
	for _, fn := range mutate {
		fn(&entry)
	}
	return taskSnapshotRow{item: entry}
}

// Generation reports the settable generation the cache reads.
func (h *bodyHarness) Generation() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.generation
}

func (h *bodyHarness) setGeneration(value uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.generation = value
}

func (h *bodyHarness) Projections() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.projections
}

func (h *bodyHarness) Encodes() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.encodes
}

func (h *bodyHarness) entryCount() int {
	h.cache.mu.Lock()
	defer h.cache.mu.Unlock()
	return len(h.cache.entries)
}

func (h *bodyHarness) hasEntry(query TaskQuery) bool {
	h.cache.mu.Lock()
	defer h.cache.mu.Unlock()
	_, ok := h.cache.entries[taskBodyKey(query)]
	return ok
}

// bodyFixture returns a fixed pair of rows, the first carrying HTML characters
// so the encoder's no-escape behaviour is observable.
func bodyFixture(_ context.Context, query TaskQuery) ([]api.TaskResponse, error) {
	return []api.TaskResponse{
		{ID: "Alpha", Title: "Alpha <b>bold</b> & <i>italic</i>"},
		{ID: "Beta", Title: "Beta " + strings.Join(query.Statuses, ",")},
	}, nil
}

func projectBody(
	project func(ctx context.Context, query TaskQuery) ([]api.TaskResponse, error),
	query TaskQuery,
) []api.TaskResponse {
	responses, err := project(context.Background(), query)
	Expect(err).To(BeNil())
	return responses
}

var _ = Describe("taskBodyCache", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("serves a held body without re-encoding", func() {
		h := newBodyHarness(bodyFixture)
		query := TaskQuery{Vaults: []string{"a"}}

		first, err := h.cache.Get(ctx, query)
		Expect(err).To(BeNil())
		second, err := h.cache.Get(ctx, query)
		Expect(err).To(BeNil())

		// Both counters are required: the encoder alone would leave "no row
		// walk, no projection" unproven.
		Expect(h.Encodes()).To(Equal(1))
		Expect(h.Projections()).To(Equal(1))
		Expect(first).To(Equal(second))
	})

	It("cached and uncached bodies are byte-identical", func() {
		queries := []TaskQuery{
			{},
			{Vaults: []string{"a"}},
			{Statuses: []string{"todo"}},
			{Phases: []string{"planning"}},
			{Vaults: []string{"a", "b"}, Statuses: []string{"next", "todo"}},
		}
		h := newBodyHarness(bodyFixture)

		for _, query := range queries {
			body, err := h.cache.Get(ctx, query)
			Expect(err).To(BeNil())
			expected, err := encodeTaskList(projectBody(bodyFixture, query))
			Expect(err).To(BeNil())
			Expect(body.Identity).To(Equal(expected))
		}

		body, err := h.cache.Get(ctx, queries[0])
		Expect(err).To(BeNil())
		// (a) HTML is not escaped.
		Expect(bytes.Contains(body.Identity, []byte("<"))).To(BeTrue())
		Expect(bytes.Contains(body.Identity, []byte(">"))).To(BeTrue())
		Expect(bytes.Contains(body.Identity, []byte("&"))).To(BeTrue())
		Expect(bytes.Contains(body.Identity, []byte(`\u003c`))).To(BeFalse())
		Expect(bytes.Contains(body.Identity, []byte(`\u003e`))).To(BeFalse())
		Expect(bytes.Contains(body.Identity, []byte(`\u0026`))).To(BeFalse())
		// (b) The encoder's trailing newline is trimmed; a JSON array ends with ].
		Expect(body.Identity[len(body.Identity)-1]).To(Equal(byte(']')))
	})

	It("builds again for a distinct query", func() {
		h := newBodyHarness(bodyFixture)

		_, err := h.cache.Get(ctx, TaskQuery{})
		Expect(err).To(BeNil())
		_, err = h.cache.Get(ctx, TaskQuery{Vaults: []string{"a"}})
		Expect(err).To(BeNil())

		Expect(h.Projections()).To(Equal(2))
		Expect(h.Encodes()).To(Equal(2))
	})

	It("drops the held entries when the generation moves", func() {
		h := newBodyHarness(bodyFixture)

		_, err := h.cache.Get(ctx, TaskQuery{})
		Expect(err).To(BeNil())
		Expect(h.Projections()).To(Equal(1))

		h.setGeneration(1)
		_, err = h.cache.Get(ctx, TaskQuery{})
		Expect(err).To(BeNil())
		Expect(h.Projections()).To(Equal(2))
		// The superseded generation's entry was dropped, not retained.
		Expect(h.entryCount()).To(Equal(1))
	})

	It("leaves the entry unheld when the generation moves during a build", func() {
		var h *bodyHarness
		h = newBodyHarness(func(_ context.Context, _ TaskQuery) ([]api.TaskResponse, error) {
			h.setGeneration(1)
			return []api.TaskResponse{{ID: "Alpha", Title: "Alpha"}}, nil
		})

		body, err := h.cache.Get(ctx, TaskQuery{})
		Expect(err).To(BeNil())
		Expect(body.Identity).NotTo(BeEmpty())
		Expect(h.hasEntry(TaskQuery{})).To(BeFalse())

		// The next read at the new generation builds again.
		_, err = h.cache.Get(ctx, TaskQuery{})
		Expect(err).To(BeNil())
		Expect(h.Projections()).To(Equal(2))
	})

	DescribeTable("keys every body-changing field",
		func(mutate func(*TaskQuery)) {
			h := newBodyHarness(bodyFixture)
			first := TaskQuery{}
			second := TaskQuery{}
			mutate(&second)

			_, err := h.cache.Get(ctx, first)
			Expect(err).To(BeNil())
			_, err = h.cache.Get(ctx, second)
			Expect(err).To(BeNil())

			Expect(h.Projections()).To(Equal(2))
			Expect(h.Encodes()).To(Equal(2))
		},
		Entry("Vaults", func(q *TaskQuery) { q.Vaults = []string{"a"} }),
		Entry("Statuses", func(q *TaskQuery) { q.Statuses = []string{"todo"} }),
		Entry("Phases", func(q *TaskQuery) { q.Phases = []string{"planning"} }),
		Entry("Assignees", func(q *TaskQuery) { q.Assignees = []string{"me"} }),
		Entry("Goals", func(q *TaskQuery) { q.Goals = []string{"g"} }),
		Entry("UpcomingHours", func(q *TaskQuery) { q.UpcomingHours = 8 }),
		Entry("SessionLive", func(q *TaskQuery) { q.SessionLive = true }),
	)

	It("gzip-compresses the identity form losslessly", func() {
		h := newBodyHarness(bodyFixture)

		body, err := h.cache.Get(ctx, TaskQuery{})
		Expect(err).To(BeNil())

		reader, err := gzip.NewReader(bytes.NewReader(body.Gzipped))
		Expect(err).To(BeNil())
		decompressed, err := io.ReadAll(reader)
		Expect(err).To(BeNil())
		Expect(reader.Close()).To(Succeed())
		Expect(decompressed).To(Equal(body.Identity))
	})

	It("stores nothing and evicts nothing when a build fails", func() {
		h := newBodyHarness(func(ctx context.Context, query TaskQuery) ([]api.TaskResponse, error) {
			if len(query.Statuses) > 0 {
				return nil, stderrors.New("boom")
			}
			return bodyFixture(ctx, query)
		})
		first := TaskQuery{}
		second := TaskQuery{Statuses: []string{"todo"}}

		held, err := h.cache.Get(ctx, first)
		Expect(err).To(BeNil())
		Expect(held.Identity).NotTo(BeEmpty())
		Expect(h.Projections()).To(Equal(1))

		failed, err := h.cache.Get(ctx, second)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("build task-list body"))
		Expect(failed).To(Equal(TaskListBody{}))
		Expect(h.hasEntry(second)).To(BeFalse())

		// The first query's entry was neither evicted nor re-projected.
		served, err := h.cache.Get(ctx, first)
		Expect(err).To(BeNil())
		Expect(served).To(Equal(held))
		Expect(h.Projections()).To(Equal(2))
	})

	It("rebuild discards held bodies", func() {
		h := newBodyHarness(bodyFixture)

		_, err := h.cache.Get(ctx, TaskQuery{})
		Expect(err).To(BeNil())
		Expect(h.Encodes()).To(Equal(1))

		h.setGeneration(1)
		_, err = h.cache.Get(ctx, TaskQuery{})
		Expect(err).To(BeNil())
		Expect(h.Encodes()).To(Equal(2))
		Expect(h.entryCount()).To(Equal(1))
	})

	It("clock boundary discards held bodies", func() {
		var h *bodyHarness
		h = newBodyHarness(
			func(_ context.Context, _ TaskQuery) ([]api.TaskResponse, error) {
				return []api.TaskResponse{{
					ID:    "Alpha",
					Title: h.clock.Now().UTC().Time().Format(time.RFC3339),
				}}, nil
			},
			func(_ context.Context, _ TaskQuery) ([]taskSnapshotRow, error) {
				return []taskSnapshotRow{bodySnapshotRow("Alpha", func(i *ops.TaskListItem) {
					i.DeferDate = bodyBaseTime.Add(2 * time.Hour).Format(time.RFC3339)
				})}, nil
			},
		)
		query := TaskQuery{UpcomingHours: 8}

		first, err := h.cache.Get(ctx, query)
		Expect(err).To(BeNil())
		Expect(h.Encodes()).To(Equal(1))

		// The boundary is now+2h, so now+1h is still inside it.
		h.clock.set(bodyBaseTime.Add(1 * time.Hour))
		second, err := h.cache.Get(ctx, query)
		Expect(err).To(BeNil())
		Expect(h.Encodes()).To(Equal(1))
		Expect(second).To(Equal(first))

		// Crossing it discards the entry and rebuilds.
		h.clock.set(bodyBaseTime.Add(3 * time.Hour))
		third, err := h.cache.Get(ctx, query)
		Expect(err).To(BeNil())
		Expect(h.Encodes()).To(Equal(2))
		Expect(third.Identity).NotTo(Equal(first.Identity))
	})

	It("never expires a body with no clock-derived rows", func() {
		rows := []taskSnapshotRow{bodySnapshotRow("Alpha")}
		Expect(taskBodyBoundary(rows, bodyBaseTime, 8)).To(BeZero())

		h := newBodyHarness(
			bodyFixture,
			func(_ context.Context, _ TaskQuery) ([]taskSnapshotRow, error) {
				return rows, nil
			},
		)
		_, err := h.cache.Get(ctx, TaskQuery{})
		Expect(err).To(BeNil())
		Expect(h.Encodes()).To(Equal(1))

		h.clock.set(bodyBaseTime.Add(24 * time.Hour))
		_, err = h.cache.Get(ctx, TaskQuery{})
		Expect(err).To(BeNil())
		Expect(h.Encodes()).To(Equal(1))
	})

	It("boundary is the earliest transition", func() {
		rows := []taskSnapshotRow{
			bodySnapshotRow("Deferred", func(i *ops.TaskListItem) {
				i.DeferDate = bodyBaseTime.Add(2 * time.Hour).Format(time.RFC3339)
			}),
			bodySnapshotRow("Done", func(i *ops.TaskListItem) {
				i.Status = "completed"
				i.CompletedDate = bodyBaseTime.Add(-1 * time.Hour).Format(time.RFC3339)
			}),
		}
		// The completed row leaves the window at now+7h; the deferred row flips
		// at now+2h, so the deferred candidate is the minimum.
		Expect(taskBodyBoundary(rows, bodyBaseTime, 8)).
			To(Equal(bodyBaseTime.Add(2 * time.Hour)))

		h := newBodyHarness(
			bodyFixture,
			func(_ context.Context, _ TaskQuery) ([]taskSnapshotRow, error) {
				return rows, nil
			},
		)
		query := TaskQuery{UpcomingHours: 8}
		_, err := h.cache.Get(ctx, query)
		Expect(err).To(BeNil())
		Expect(h.Encodes()).To(Equal(1))

		h.clock.set(bodyBaseTime.Add(1*time.Hour + 59*time.Minute))
		_, err = h.cache.Get(ctx, query)
		Expect(err).To(BeNil())
		Expect(h.Encodes()).To(Equal(1))

		h.clock.set(bodyBaseTime.Add(2*time.Hour + time.Minute))
		_, err = h.cache.Get(ctx, query)
		Expect(err).To(BeNil())
		Expect(h.Encodes()).To(Equal(2))
	})

	It("uses the modified date when a completed row has no completed date", func() {
		rows := []taskSnapshotRow{
			bodySnapshotRow("NoDates", func(i *ops.TaskListItem) {
				i.Status = "completed"
			}),
			bodySnapshotRow("ModifiedOnly", func(i *ops.TaskListItem) {
				i.Status = "completed"
				i.ModifiedDate = bodyBaseTime.Add(-2 * time.Hour).Format(time.RFC3339)
			}),
		}
		// The row with neither date is skipped; the modified-only row leaves the
		// window at now+6h.
		Expect(taskBodyBoundary(rows, bodyBaseTime, 8)).
			To(Equal(bodyBaseTime.Add(6 * time.Hour)))
	})

	It("excludes a boundary already in the past", func() {
		rows := []taskSnapshotRow{
			bodySnapshotRow("Past", func(i *ops.TaskListItem) {
				i.DeferDate = bodyBaseTime.Add(-1 * time.Hour).Format(time.RFC3339)
			}),
			bodySnapshotRow("BadDefer", func(i *ops.TaskListItem) {
				i.DeferDate = "not-a-date"
			}),
		}
		Expect(taskBodyBoundary(rows, bodyBaseTime, 8)).To(BeZero())
	})

	It("invalidates when a hidden row becomes visible", func() {
		deferAt := bodyBaseTime.Add(9 * time.Hour)
		rows := []taskSnapshotRow{bodySnapshotRow("Hidden", func(i *ops.TaskListItem) {
			i.DeferDate = deferAt.Format(time.RFC3339)
		})}
		var h *bodyHarness
		h = newBodyHarness(
			func(_ context.Context, q TaskQuery) ([]api.TaskResponse, error) {
				now := h.clock.Now().UTC().Time()
				cutoff := now.Add(time.Duration(q.UpcomingHours) * time.Hour)
				if deferAt.After(cutoff) {
					return []api.TaskResponse{}, nil
				}
				return []api.TaskResponse{{ID: "Hidden"}}, nil
			},
			func(_ context.Context, _ TaskQuery) ([]taskSnapshotRow, error) {
				return rows, nil
			},
		)
		query := TaskQuery{UpcomingHours: 8}

		// The row is beyond now+8h, so visibleRow drops it and the body omits
		// it. Its entry instant is defer-upcoming = now+1h.
		first, err := h.cache.Get(ctx, query)
		Expect(err).To(BeNil())
		Expect(h.Encodes()).To(Equal(1))
		Expect(bytes.Contains(first.Identity, []byte("Hidden"))).To(BeFalse())

		h.clock.set(bodyBaseTime.Add(2 * time.Hour))
		second, err := h.cache.Get(ctx, query)
		Expect(err).To(BeNil())
		Expect(h.Encodes()).To(Equal(2))
		Expect(bytes.Contains(second.Identity, []byte("Hidden"))).To(BeTrue())
	})

	It("keeps the lookback and upcoming windows", func() {
		Expect(LookbackHours).To(Equal(8))
		Expect(DefaultUpcomingHours).To(Equal(8))
	})
})

// bodyRowsVaults is a VaultsProvider stub for the taskSnapshotRows tests.
type bodyRowsVaults struct {
	vaults []Vault
	err    error
}

func (v bodyRowsVaults) Vaults(_ context.Context) ([]Vault, error) {
	return v.vaults, v.err
}

// bodyRowsRevisions is an IndexRevisions stub whose revision never moves, so
// the store takes its full-build path.
type bodyRowsRevisions struct{}

func (bodyRowsRevisions) Revision(_ pageindex.Key) uint64 { return 1 }

func (bodyRowsRevisions) ChangedPagesSince(
	_ pageindex.Key, _ uint64,
) ([]string, bool) {
	return nil, false
}

func (bodyRowsRevisions) ListPages(
	_ context.Context, _, _ string,
) ([]*domain.Page, error) {
	return nil, nil
}

// bodyRowsGenerations is a fixed generationSource.
type bodyRowsGenerations uint64

func (g bodyRowsGenerations) Generation() uint64 { return uint64(g) }

var _ = Describe("taskSnapshotRows", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	newBoard := func(vaults []Vault) *board {
		return &board{
			vaults: bodyRowsVaults{vaults: vaults},
			snapshot: newTaskSnapshotStore(taskSnapshotParams{
				Build: func(_ context.Context, vault Vault) ([]taskSnapshotRow, error) {
					return []taskSnapshotRow{{item: ops.TaskListItem{Name: vault.Name}}}, nil
				},
				Revisions:   bodyRowsRevisions{},
				Generations: bodyRowsGenerations(0),
			}),
		}
	}

	It("concatenates the selected vaults' pre-filter rows", func() {
		b := newBoard([]Vault{
			{Name: "a", Path: "/a", TasksFolder: "24 Tasks"},
			{Name: "b", Path: "/b", TasksFolder: "24 Tasks"},
		})

		rows, err := b.taskSnapshotRows(ctx, TaskQuery{})
		Expect(err).To(BeNil())
		Expect(rows).To(HaveLen(2))

		filtered, err := b.taskSnapshotRows(ctx, TaskQuery{Vaults: []string{"b"}})
		Expect(err).To(BeNil())
		Expect(filtered).To(HaveLen(1))
		Expect(filtered[0].item.Name).To(Equal("b"))
	})

	It("wraps a vaults read failure", func() {
		b := &board{vaults: bodyRowsVaults{err: stderrors.New("boom")}}

		_, err := b.taskSnapshotRows(ctx, TaskQuery{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("list vaults"))
	})
})
