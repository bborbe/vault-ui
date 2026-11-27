// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board_test

import (
	"context"
	"encoding/json"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-cli/pkg/ops"

	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/board"
	"github.com/bborbe/vault-ui/pkg/openable"
)

// fakeHolds is an openable.Lookup keyed by openable.Key.
type fakeHolds struct {
	mu         sync.Mutex
	holds      map[string]*api.HoldResponse
	generation uint64
}

func newFakeHolds() *fakeHolds {
	return &fakeHolds{holds: map[string]*api.HoldResponse{}}
}

func (f *fakeHolds) set(vaultName, kind, name string, hold *api.HoldResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holds[openable.Key(vaultName, kind, name)] = hold
	f.generation++
}

func (f *fakeHolds) Hold(vaultName, kind, name string) *api.HoldResponse {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.holds[openable.Key(vaultName, kind, name)]
}

func (f *fakeHolds) Generation() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.generation
}

func stalled() *api.HoldResponse {
	return &api.HoldResponse{Reason: "progress-stalled", Label: "⏸ stalled", Blockers: []string{}}
}

var _ = Describe("hold_reason join", func() {
	var (
		h     *harness
		holds *fakeHolds
	)

	BeforeEach(func() {
		h = newHarness(item("Held Task"), item("Free Task"))
		holds = newFakeHolds()
		h.holds = holds
		h.build()
	})

	It("joins a task by the wrapper's vault-cli vault name, kind and name", func() {
		holds.set(vault.VaultName, "task", "Held Task", stalled())

		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		byID := map[string]api.TaskResponse{}
		for _, response := range responses {
			byID[response.ID] = response
		}
		Expect(byID["Held Task"].HoldReason).NotTo(BeNil())
		Expect(byID["Held Task"].HoldReason.Reason).To(Equal("progress-stalled"))
		Expect(byID["Free Task"].HoldReason).To(BeNil())
	})

	It("falls back to the display vault name", func() {
		holds.set(vault.Name, "task", "Held Task", stalled())

		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		var found *api.HoldResponse
		for _, response := range responses {
			if response.ID == "Held Task" {
				found = response.HoldReason
			}
		}
		Expect(found).NotTo(BeNil())
	})

	It("does not join a task to a goal hold of the same name", func() {
		holds.set(vault.VaultName, "goal", "Held Task", stalled())

		responses, err := h.board.ListTasks(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		for _, response := range responses {
			Expect(response.HoldReason).To(BeNil())
		}
	})

	It("serves hold_reason as null when no lookup is wired", func() {
		h.holds = nil
		h.build()

		body, err := h.board.ListTasksBody(context.Background(), board.TaskQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		var rows []map[string]json.RawMessage
		Expect(json.Unmarshal(body.Identity, &rows)).To(Succeed())
		Expect(string(rows[0]["hold_reason"])).To(Equal("null"))
	})

	It("rebuilds a held task body when the held set changes", func() {
		query := board.TaskQuery{UpcomingHours: 8}
		first, err := h.board.ListTasksBody(context.Background(), query)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(first.Identity)).NotTo(ContainSubstring("progress-stalled"))

		holds.set(vault.VaultName, "task", "Held Task", stalled())

		second, err := h.board.ListTasksBody(context.Background(), query)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(second.Identity)).To(ContainSubstring(`"reason":"progress-stalled"`))
	})

	It("joins goals and topics by their own kind", func() {
		h.list.items["23 Goals"] = []ops.TaskListItem{{Name: "Held Goal", Status: "in_progress"}}
		h.list.items["23 Topics"] = []ops.TaskListItem{{Name: "Held Topic", Status: "in_progress"}}
		holds.set(vault.VaultName, "goal", "Held Goal",
			&api.HoldResponse{Reason: "parked-until", Label: "⏰ parked", Blockers: []string{}})
		holds.set(vault.VaultName, "topic", "Held Topic",
			&api.HoldResponse{Reason: "blocked", Label: "⛔ blocked", Blockers: []string{}})

		goals, err := h.board.ListGoals(context.Background(), board.GoalQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(goals).To(HaveLen(1))
		Expect(goals[0].HoldReason).NotTo(BeNil())
		Expect(goals[0].HoldReason.Reason).To(Equal("parked-until"))

		topics, err := h.board.ListTopics(context.Background(), board.TopicQuery{UpcomingHours: 8})
		Expect(err).NotTo(HaveOccurred())
		Expect(topics).To(HaveLen(1))
		Expect(topics[0].HoldReason).NotTo(BeNil())
		Expect(topics[0].HoldReason.Reason).To(Equal("blocked"))
	})
})
