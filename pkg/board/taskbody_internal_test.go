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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/api"
)

// bodyHarness wires a taskBodyCache to counting seams: the projection and encode
// counters prove a hit performs neither, and the generation is settable so a
// test can move it.
type bodyHarness struct {
	cache *taskBodyCache

	mu          sync.Mutex
	projections int
	encodes     int
	generation  uint64
}

func newBodyHarness(
	project func(ctx context.Context, query TaskQuery) ([]api.TaskResponse, error),
) *bodyHarness {
	h := &bodyHarness{}
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
	})
	return h
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
})
