// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"
	stderrors "errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-cli/pkg/ops"
)

func TestVaultAPI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "VaultAPI Suite")
}

// fakeClient serves one mutable task list; the poll reads all three lists.
type fakeClient struct {
	tasks []Task
}

func (f *fakeClient) BaseURL() string { return "http://127.0.0.1:1" }

func (f *fakeClient) ListTasks(_ context.Context) ([]Task, error) { return f.tasks, nil }

func (f *fakeClient) ListGoals(_ context.Context) ([]Goal, error) { return nil, nil }

func (f *fakeClient) ListTopics(_ context.Context) ([]Topic, error) { return nil, nil }

// failingClient fails every list read.
type failingClient struct{}

func (f *failingClient) BaseURL() string { return "http://127.0.0.1:1" }

func (f *failingClient) ListTasks(_ context.Context) ([]Task, error) {
	return nil, stderrors.New("vault-api unreachable")
}

func (f *failingClient) ListGoals(_ context.Context) ([]Goal, error) {
	return nil, stderrors.New("vault-api unreachable")
}

func (f *failingClient) ListTopics(_ context.Context) ([]Topic, error) {
	return nil, stderrors.New("vault-api unreachable")
}

var _ = Describe("Source", func() {
	It("runs the change callback only when a poll's content differs", func() {
		client := &fakeClient{tasks: []Task{{Title: "First", Vault: "personal"}}}
		source := NewSource(client)
		changes := 0
		source.SetOnChange(func() { changes++ })

		// The first poll publishes content, so it counts as a change.
		source.poll(context.Background())
		Expect(changes).To(Equal(1))
		revision := source.Revision(SnapshotKey{})

		// An identical poll changes nothing: no callback, no revision move.
		source.poll(context.Background())
		Expect(changes).To(Equal(1))
		Expect(source.Revision(SnapshotKey{})).To(Equal(revision))

		// New content moves the revision and fires once.
		client.tasks = append(client.tasks, Task{Title: "Second", Vault: "personal"})
		source.poll(context.Background())
		Expect(changes).To(Equal(2))
		Expect(source.Revision(SnapshotKey{})).To(BeNumerically(">", revision))
	})

	It("records unavailability on a failed poll", func() {
		source := NewSource(&failingClient{})
		source.poll(context.Background())
		Expect(source.Unavailable()).To(BeTrue())
		Expect(source.Tasks("personal")).To(BeEmpty())
	})
})

var _ = Describe("NewListOperation", func() {
	// One operation serves every kind; the folder argument picks the list. A
	// version that always read the task list served goals and topics from tasks.
	newOperation := func() ops.ListOperation {
		source := NewSourceFromData(
			[]Task{{Title: "Task A", Status: "in_progress", Vault: "personal"}},
			[]Goal{{Title: "Goal A", Status: "in_progress", Vault: "personal"}},
			[]Topic{{Title: "Topic A", Status: "in_progress", Assignee: "ben", Vault: "personal"}},
		)
		return NewListOperation(source, "personal", "25 Tasks", "24 Goals", "23 Topics")
	}

	It("returns tasks, goals and topics by folder", func() {
		for folder, expected := range map[string]string{
			"25 Tasks":  "Task A",
			"24 Goals":  "Goal A",
			"23 Topics": "Topic A",
		} {
			items, err := newOperation().Execute(
				context.Background(), "/vault", "personal", folder, nil, true, "", "",
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(items).To(HaveLen(1), folder)
			Expect(items[0].Name).To(Equal(expected), folder)
		}
	})

	It("carries a topic's assignee", func() {
		items, err := newOperation().Execute(
			context.Background(), "/vault", "personal", "23 Topics", nil, true, "", "",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(items[0].Assignee).To(Equal("ben"))
	})
})
