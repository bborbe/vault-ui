// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package launchregistry_test

import (
	"strconv"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/launchregistry"
)

var _ = Describe("LaunchRegistry", func() {
	var registry launchregistry.Registry

	BeforeEach(func() {
		registry = launchregistry.NewRegistry()
	})

	DescribeTable("EvictIfFinished",
		func(body func()) {
			body()
		},
		Entry("evict-if-finished-drops-only-a-finished-record", func() {
			registry.Begin("vault-a", "finished", "task")
			registry.Finish("vault-a", "finished")
			Expect(registry.EvictIfFinished("vault-a", "finished")).To(BeTrue())
			_, ok := registry.State("vault-a", "finished")
			Expect(ok).To(BeFalse())
			Expect(registry.Size()).To(Equal(0))

			registry.Begin("vault-a", "in-flight", "task")
			Expect(registry.EvictIfFinished("vault-a", "in-flight")).To(BeFalse())
			state, ok := registry.State("vault-a", "in-flight")
			Expect(ok).To(BeTrue())
			Expect(state).To(Equal(launchregistry.InFlight))
			Expect(registry.Size()).To(Equal(1))
		}),
	)

	It("begin records in-flight and grows size", func() {
		registry.Begin("vault-a", "item", "task")
		state, ok := registry.State("vault-a", "item")
		Expect(ok).To(BeTrue())
		Expect(state).To(Equal(launchregistry.InFlight))
		Expect(registry.Size()).To(Equal(1))
	})

	It("begin then finish yields finished and preserves the kind", func() {
		registry.Begin("vault-a", "item", "task")
		registry.Finish("vault-a", "item")
		state, ok := registry.State("vault-a", "item")
		Expect(ok).To(BeTrue())
		Expect(state).To(Equal(launchregistry.Finished))
		Expect(registry.Finished("vault-a")).To(ConsistOf(
			launchregistry.FinishedRecord{ItemID: "item", Kind: "task"},
		))
	})

	It("state on an unknown key returns not-found", func() {
		state, ok := registry.State("vault-a", "nope")
		Expect(ok).To(BeFalse())
		Expect(state).To(Equal(launchregistry.State("")))
	})

	It("evict drops a record", func() {
		registry.Begin("vault-a", "item", "task")
		registry.Finish("vault-a", "item")
		registry.Evict("vault-a", "item")
		_, ok := registry.State("vault-a", "item")
		Expect(ok).To(BeFalse())
		Expect(registry.Size()).To(Equal(0))
	})

	It("evict-if-finished on an absent key returns false", func() {
		Expect(registry.EvictIfFinished("vault-a", "nope")).To(BeFalse())
		Expect(registry.Size()).To(Equal(0))
	})

	It("finished filters by vault and excludes in-flight", func() {
		registry.Begin("vault-a", "done", "task")
		registry.Begin("vault-a", "running", "task")
		registry.Begin("vault-b", "other", "goal")
		registry.Finish("vault-a", "done")
		registry.Finish("vault-b", "other")
		Expect(registry.Finished("vault-a")).To(ConsistOf(
			launchregistry.FinishedRecord{ItemID: "done", Kind: "task"},
		))
	})

	It("two begins for one key leave one record with the last kind", func() {
		registry.Begin("vault-a", "item", "task")
		registry.Begin("vault-a", "item", "goal")
		Expect(registry.Size()).To(Equal(1))
		registry.Finish("vault-a", "item")
		Expect(registry.Finished("vault-a")).To(ConsistOf(
			launchregistry.FinishedRecord{ItemID: "item", Kind: "goal"},
		))
	})

	It("finish with no record is a no-op", func() {
		registry.Finish("vault-a", "missing")
		_, ok := registry.State("vault-a", "missing")
		Expect(ok).To(BeFalse())
		Expect(registry.Size()).To(Equal(0))
	})

	It("finish after evict does not panic", func() {
		registry.Begin("vault-a", "item", "task")
		registry.Finish("vault-a", "item")
		registry.Evict("vault-a", "item")
		registry.Finish("vault-a", "item")
		Expect(registry.Size()).To(Equal(0))
	})

	It("size counts across vaults", func() {
		registry.Begin("vault-a", "one", "task")
		registry.Begin("vault-a", "two", "task")
		registry.Begin("vault-b", "three", "goal")
		Expect(registry.Size()).To(Equal(3))
	})

	It("mark taken over then a fresh begin clears the mark", func() {
		registry.Begin("vault-a", "item", "task")
		Expect(registry.WasTakenOver("vault-a", "item")).To(BeFalse())

		registry.MarkTakenOver("vault-a", "item")
		Expect(registry.WasTakenOver("vault-a", "item")).To(BeTrue())

		registry.Begin("vault-a", "item", "task")
		Expect(registry.WasTakenOver("vault-a", "item")).To(BeFalse())
	})

	It("is safe under concurrent access to disjoint keys", func() {
		const workers = 32
		results := make(chan bool, workers)
		var wg sync.WaitGroup
		wg.Add(workers)
		for i := 0; i < workers; i++ {
			go func(index int) {
				defer wg.Done()
				itemID := "item-" + strconv.Itoa(index)
				registry.Begin("vault-a", itemID, "task")
				registry.Finish("vault-a", itemID)
				results <- registry.EvictIfFinished("vault-a", itemID)
			}(i)
		}
		wg.Wait()
		close(results)
		for evicted := range results {
			Expect(evicted).To(BeTrue())
		}
		Expect(registry.Size()).To(Equal(0))
	})
})
