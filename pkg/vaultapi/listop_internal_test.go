// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("listOperation", func() {
	source := NewSourceFromData(
		[]Task{},
		[]Goal{
			{
				Title: "A Goal", Status: "next", Phase: "planning", Vault: "test",
				Assignee: "ben", Priority: 2,
			},
			{Title: "Other Goal", Status: "next", Assignee: "anna", Vault: "test"},
		},
		[]Topic{
			{
				Title: "A Topic", Status: "in_progress", Phase: "execution", Vault: "test",
				Priority: 3,
			},
		},
		nil,
	)
	op := NewListOperation(source, "test", "25 Tasks", "24 Goals", "23 Topics")

	It("carries a goal's phase onto the list item", func() {
		items, err := op.Execute(context.Background(), "", "test", "24 Goals", nil, true, "", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(HaveLen(2))
		Expect(items[0].Phase).To(Equal("planning"))
	})

	It("carries a goal's assignee and priority onto the list item", func() {
		items, err := op.Execute(context.Background(), "", "test", "24 Goals", nil, true, "", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(items[0].Assignee).To(Equal("ben"))
		Expect(items[0].Priority).To(Equal(2))
	})

	It("filters goals by assignee", func() {
		items, err := op.Execute(
			context.Background(), "", "test", "24 Goals", nil, true, "anna", "",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(HaveLen(1))
		Expect(items[0].Name).To(Equal("Other Goal"))
	})

	It("carries a topic's phase onto the list item", func() {
		items, err := op.Execute(context.Background(), "", "test", "23 Topics", nil, true, "", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(HaveLen(1))
		Expect(items[0].Phase).To(Equal("execution"))
		Expect(items[0].Priority).To(Equal(3))
	})
})
