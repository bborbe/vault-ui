// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
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

func (f *fakeClient) ListTaskEnvelope(_ context.Context) (taskList, error) {
	return taskList{Items: f.tasks}, nil
}

func (f *fakeClient) ListTasks(_ context.Context) ([]Task, error) { return f.tasks, nil }

func (f *fakeClient) ListGoals(_ context.Context) ([]Goal, error) { return nil, nil }

func (f *fakeClient) ListTopics(_ context.Context) ([]Topic, error) { return nil, nil }

func (f *fakeClient) Page(_ context.Context, _ string, _ string) (*Page, error) {
	return nil, nil
}

func (f *fakeClient) Backlinks(_ context.Context, _ string, _ string) ([]Backlink, error) {
	return nil, nil
}

// failingClient fails every list read.
type failingClient struct{}

func (f *failingClient) BaseURL() string { return "http://127.0.0.1:1" }

func (f *failingClient) ListTaskEnvelope(_ context.Context) (taskList, error) {
	return taskList{}, stderrors.New("vault-api unreachable")
}

func (f *failingClient) ListTasks(_ context.Context) ([]Task, error) {
	return nil, stderrors.New("vault-api unreachable")
}

func (f *failingClient) ListGoals(_ context.Context) ([]Goal, error) {
	return nil, stderrors.New("vault-api unreachable")
}

func (f *failingClient) ListTopics(_ context.Context) ([]Topic, error) {
	return nil, stderrors.New("vault-api unreachable")
}

func (f *failingClient) Page(_ context.Context, _ string, _ string) (*Page, error) {
	return nil, stderrors.New("vault-api unreachable")
}

func (f *failingClient) Backlinks(_ context.Context, _ string, _ string) ([]Backlink, error) {
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

	It("Refresh polls synchronously so a read after it sees the new rows", func() {
		client := &fakeClient{tasks: []Task{{Title: "First", Vault: "personal"}}}
		source := NewSource(client)
		Expect(source.Refresh(context.Background())).To(Succeed())
		Expect(source.Tasks("personal")).To(HaveLen(1))

		client.tasks = append(client.tasks, Task{Title: "Second", Vault: "personal"})
		Expect(source.Refresh(context.Background())).To(Succeed())
		Expect(source.Tasks("personal")).To(HaveLen(2))
	})

	It("Refresh returns the fetch error and keeps the last-good rows", func() {
		source := NewSourceFromData([]Task{{Title: "Kept", Vault: "personal"}}, nil, nil, nil)
		source.client = &failingClient{}
		Expect(source.Refresh(context.Background())).To(HaveOccurred())
		Expect(source.Unavailable()).To(BeTrue())
		Expect(source.Tasks("personal")).To(HaveLen(1))
	})

	It("Refresh is a no-op for a source with no client", func() {
		source := NewSourceFromData(nil, nil, nil, nil)
		Expect(source.Refresh(context.Background())).To(Succeed())
	})

	It("records unavailability on a failed poll", func() {
		source := NewSource(&failingClient{})
		source.poll(context.Background())
		Expect(source.Unavailable()).To(BeTrue())
		Expect(source.Tasks("personal")).To(BeEmpty())
	})
})

var _ = Describe("Source coverage set", func() {
	// pollingSource drives the real client against an httptest body, so the
	// `json:"vaults"` tag is exercised by a real response rather than a
	// hand-built taskList — the warnings-count failure mode in reverse.
	pollingSource := func(tasksBody string) *Source {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/api/v1/tasks" {
				_, _ = w.Write([]byte(tasksBody))
				return
			}
			_, _ = w.Write([]byte(`{"items":[]}`))
		}))
		DeferCleanup(server.Close)
		return NewSource(New(Config{BaseURL: server.URL}))
	}

	It("populates IndexedVaults from a body carrying vaults", func() {
		source := pollingSource(`{"items":[],"vaults":["personal","work"]}`)
		Expect(source.poll(context.Background())).To(Succeed())
		Expect(source.IndexedVaults()).To(Equal([]string{"personal", "work"}))
	})

	It("leaves IndexedVaults nil when the body omits vaults", func() {
		source := pollingSource(`{"items":[]}`)
		Expect(source.poll(context.Background())).To(Succeed())
		Expect(source.IndexedVaults()).To(BeNil())
	})

	It("keeps an empty coverage set non-nil", func() {
		source := pollingSource(`{"items":[],"vaults":[]}`)
		Expect(source.poll(context.Background())).To(Succeed())
		Expect(source.IndexedVaults()).NotTo(BeNil())
		Expect(source.IndexedVaults()).To(BeEmpty())
	})

	It("returns a copy the caller cannot mutate", func() {
		source := pollingSource(`{"items":[],"vaults":["personal"]}`)
		Expect(source.poll(context.Background())).To(Succeed())
		first := source.IndexedVaults()
		first[0] = "mutated"
		Expect(source.IndexedVaults()).To(Equal([]string{"personal"}))
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
			nil,
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

	It("refuses a folder that is none of the three", func() {
		_, err := newOperation().Execute(
			context.Background(), "/vault", "personal", "99 Elsewhere", nil, true, "", "",
		)
		Expect(err).To(HaveOccurred())
	})
})
