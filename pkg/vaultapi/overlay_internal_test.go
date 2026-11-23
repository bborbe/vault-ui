// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"
	"sync"
	"time"

	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Overlay", func() {
	var (
		clock   libtime.CurrentDateTime
		start   time.Time
		overlay *Overlay
	)
	BeforeEach(func() {
		start = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
		clock = libtime.NewCurrentDateTime()
		clock.SetNow(libtime.DateTime(start))
		overlay = NewOverlay(clock, DefaultPendingTTL)
	})

	It("overlays pending values while vault-api is stale", func() {
		overlay.Record("v", KindTask, "T", PendingFields{
			Flag: boolPtr(true), Assignee: strPtr("ben"),
		})
		got := overlay.Apply("v", KindTask, "T", OverlayValues{Status: "next"})
		Expect(got).To(Equal(OverlayValues{Flag: true, Assignee: "ben", Status: "next"}))
		Expect(overlay.Len()).To(Equal(1))
	})

	It("leaves other items and kinds untouched", func() {
		overlay.Record("v", KindTask, "T", PendingFields{Flag: boolPtr(true)})
		Expect(overlay.Apply("v", KindGoal, "T", OverlayValues{}).Flag).To(BeFalse())
		Expect(overlay.Apply("w", KindTask, "T", OverlayValues{}).Flag).To(BeFalse())
	})

	It("drops the entry once vault-api reports the written values", func() {
		overlay.Record("v", KindTask, "T", PendingFields{Phase: strPtr("execution")})
		got := overlay.Apply("v", KindTask, "T", OverlayValues{Phase: "execution"})
		Expect(got.Phase).To(Equal("execution"))
		Expect(overlay.Len()).To(Equal(0))
		// a later revert on vault-api's side is no longer masked
		Expect(overlay.Apply("v", KindTask, "T", OverlayValues{Phase: "todo"}).Phase).
			To(Equal("todo"))
	})

	It("keeps fields vault-api has not caught up on", func() {
		overlay.Record("v", KindTask, "T", PendingFields{
			Phase: strPtr("done"), Status: strPtr("completed"),
		})
		got := overlay.Apply("v", KindTask, "T", OverlayValues{Phase: "done", Status: "in_progress"})
		Expect(got.Status).To(Equal("completed"))
		Expect(overlay.Len()).To(Equal(1))
	})

	It("expires an entry after the TTL", func() {
		overlay.Record("v", KindTask, "T", PendingFields{Assignee: strPtr("ben")})
		clock.SetNow(libtime.DateTime(start.Add(DefaultPendingTTL)))
		Expect(overlay.Apply("v", KindTask, "T", OverlayValues{}).Assignee).To(BeEmpty())
		Expect(overlay.Len()).To(Equal(0))
	})

	It("overlays a cleared field as empty or false", func() {
		overlay.Record("v", KindGoal, "G", PendingFields{
			Flag: boolPtr(false), Assignee: strPtr(""), ClaudeSessionID: strPtr(""),
		})
		got := overlay.Apply("v", KindGoal, "G", OverlayValues{
			Flag: true, Assignee: "ben", ClaudeSessionID: "abc",
		})
		Expect(got).To(Equal(OverlayValues{}))
	})

	It("lets a later write win over an earlier pending one", func() {
		overlay.Record("v", KindTask, "T", PendingFields{Flag: boolPtr(true)})
		overlay.Record("v", KindTask, "T", PendingFields{Flag: boolPtr(false)})
		Expect(overlay.Apply("v", KindTask, "T", OverlayValues{Flag: true}).Flag).To(BeFalse())
	})

	It("is safe for concurrent use", func() {
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				overlay.Record("v", KindTask, "T", PendingFields{Flag: boolPtr(true)})
			}()
			go func() {
				defer wg.Done()
				_ = overlay.Apply("v", KindTask, "T", OverlayValues{})
			}()
		}
		wg.Wait()
	})
})

var _ = Describe("listOperation with pending writes", func() {
	It("serves the pending value until vault-api catches up", func() {
		source := NewSourceFromData(
			[]Task{{Title: "T", Status: "next", Vault: "test"}},
			[]Goal{{Title: "G", Vault: "test", Flag: true}},
			[]Topic{{Title: "P", Vault: "test"}},
		)
		source.RecordPending("test", KindTask, "T", PendingFields{Assignee: strPtr("ben")})
		source.RecordPending("test", KindGoal, "G", PendingFields{Flag: boolPtr(false)})
		source.RecordPending("test", KindTopic, "P", PendingFields{Phase: strPtr("execution")})
		op := NewListOperation(source, "test", "25 Tasks", "24 Goals", "23 Topics")
		ctx := context.Background()

		tasks, err := op.Execute(ctx, "", "test", "25 Tasks", nil, true, "", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(tasks[0].Assignee).To(Equal("ben"))
		goals, err := op.Execute(ctx, "", "test", "24 Goals", nil, true, "", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(goals[0].Flag).To(BeFalse())
		topics, err := op.Execute(ctx, "", "test", "23 Topics", nil, true, "", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(topics[0].Phase).To(Equal("execution"))
	})
})

func strPtr(v string) *string { return &v }

func boolPtr(v bool) *bool { return &v }
