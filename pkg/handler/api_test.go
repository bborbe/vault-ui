// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/board"
	"github.com/bborbe/vault-ui/pkg/handler"
	"github.com/bborbe/vault-ui/pkg/websocket"
)

type fakeBoard struct {
	vaults    []api.VaultResponse
	assignees api.AssigneesResponse
	tasks     []api.TaskResponse
	goals     []api.GoalResponse
	topics    []api.TopicResponse
	topic     api.TopicDetailResponse
	err       error

	gotVaults     []string
	gotTaskQuery  board.TaskQuery
	gotGoalQuery  board.GoalQuery
	gotTopicVault string
	gotTopicID    string
}

func (f *fakeBoard) ListVaults(_ context.Context) ([]api.VaultResponse, error) {
	return f.vaults, f.err
}

func (f *fakeBoard) ListAssignees(_ context.Context, vaults []string) (api.AssigneesResponse, error) {
	f.gotVaults = vaults
	return f.assignees, f.err
}

func (f *fakeBoard) ListTasks(_ context.Context, query board.TaskQuery) ([]api.TaskResponse, error) {
	f.gotTaskQuery = query
	return f.tasks, f.err
}

func (f *fakeBoard) ListGoals(_ context.Context, query board.GoalQuery) ([]api.GoalResponse, error) {
	f.gotGoalQuery = query
	return f.goals, f.err
}

func (f *fakeBoard) ListTopics(_ context.Context, vaults []string) ([]api.TopicResponse, error) {
	f.gotVaults = vaults
	return f.topics, f.err
}

func (f *fakeBoard) ShowTopic(
	_ context.Context,
	vault, topicID string,
) (api.TopicDetailResponse, error) {
	f.gotTopicVault = vault
	f.gotTopicID = topicID
	return f.topic, f.err
}

func newRouter(fake *fakeBoard) http.Handler {
	staticFS := fstest.MapFS{
		"index.html": {Data: []byte("<html>index</html>")},
		"app.js":     {Data: []byte("console.log('app')")},
		"style.css":  {Data: []byte("body{}")},
	}
	readiness := vaultui.NewReadiness()
	readiness.SetReady()
	manager := websocket.NewConnectionManager(websocket.NewMetrics())
	return handler.CreateHTTPRouter(fake, &fakeMutations{}, staticFS, readiness, manager)
}

func doGet(router http.Handler, target string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder
}

func decodeDetail(body io.Reader) string {
	var detail api.DetailResponse
	_ = json.NewDecoder(body).Decode(&detail)
	return detail.Detail
}

var _ = Describe("API router", func() {
	var fake *fakeBoard
	var router http.Handler

	BeforeEach(func() {
		fake = &fakeBoard{
			vaults:    []api.VaultResponse{},
			tasks:     []api.TaskResponse{},
			goals:     []api.GoalResponse{},
			topics:    []api.TopicResponse{},
			assignees: api.AssigneesResponse{Named: []string{}},
		}
		router = newRouter(fake)
	})

	DescribeTable("serves the read routes",
		func(target string, status int, assert func(*httptest.ResponseRecorder)) {
			recorder := doGet(router, target)
			Expect(recorder.Code).To(Equal(status))
			assert(recorder)
		},
		Entry("vaults", "/api/vaults", http.StatusOK, func(r *httptest.ResponseRecorder) {
			Expect(r.Body.String()).To(Equal("[]"))
		}),
		Entry("assignees", "/api/assignees", http.StatusOK, func(r *httptest.ResponseRecorder) {
			Expect(r.Body.String()).To(ContainSubstring("has_unassigned"))
		}),
		Entry("tasks", "/api/tasks", http.StatusOK, func(r *httptest.ResponseRecorder) {
			Expect(r.Body.String()).To(Equal("[]"))
		}),
		Entry("goals", "/api/goals", http.StatusOK, func(r *httptest.ResponseRecorder) {
			Expect(r.Body.String()).To(Equal("[]"))
		}),
		Entry("topics", "/api/topics", http.StatusOK, func(r *httptest.ResponseRecorder) {
			Expect(r.Body.String()).To(Equal("[]"))
		}),
		Entry("topic detail", "/api/topics/TopicOne?vault=personal", http.StatusOK,
			func(r *httptest.ResponseRecorder) {
				Expect(r.Body.String()).To(ContainSubstring("unresolved"))
			}),
	)

	It("passes the repeated vault query values through", func() {
		doGet(router, "/api/assignees?vault=a&vault=b")
		Expect(fake.gotVaults).To(Equal([]string{"a", "b"}))
	})

	It("passes every task query parameter through", func() {
		doGet(router, "/api/tasks?vault=a&status=x&phase=y&assignee=z&goal=g&upcoming_hours=12&session_live=true")
		Expect(fake.gotTaskQuery.Vaults).To(Equal([]string{"a"}))
		Expect(fake.gotTaskQuery.Statuses).To(Equal([]string{"x"}))
		Expect(fake.gotTaskQuery.Phases).To(Equal([]string{"y"}))
		Expect(fake.gotTaskQuery.Assignees).To(Equal([]string{"z"}))
		Expect(fake.gotTaskQuery.Goals).To(Equal([]string{"g"}))
		Expect(fake.gotTaskQuery.UpcomingHours).To(Equal(12))
		Expect(fake.gotTaskQuery.SessionLive).To(BeTrue())
	})

	It("defaults upcoming_hours to 8", func() {
		doGet(router, "/api/tasks")
		Expect(fake.gotTaskQuery.UpcomingHours).To(Equal(8))
		Expect(fake.gotTaskQuery.SessionLive).To(BeFalse())
	})

	DescribeTable("rejects an invalid upcoming_hours with the FastAPI body",
		func(query, expectedType string) {
			recorder := doGet(router, "/api/tasks?upcoming_hours="+query)
			Expect(recorder.Code).To(Equal(http.StatusUnprocessableEntity))
			Expect(recorder.Body.String()).To(ContainSubstring(expectedType))
		},
		Entry("too large", "200", "less_than_equal"),
		Entry("negative", "-1", "greater_than_equal"),
		Entry("not a number", "abc", "int_parsing"),
	)

	It("requires vault on the topic detail route", func() {
		recorder := doGet(router, "/api/topics/TopicOne")
		Expect(recorder.Code).To(Equal(http.StatusUnprocessableEntity))
		Expect(recorder.Body.String()).To(ContainSubstring(`"type":"missing"`))
		Expect(recorder.Body.String()).To(ContainSubstring(`["query","vault"]`))
	})

	It("maps an unknown vault to 404 with the Python detail", func() {
		fake.err = board.UnknownVaultError{Vault: "Nope"}
		recorder := doGet(router, "/api/topics/TopicOne?vault=Nope")
		Expect(recorder.Code).To(Equal(http.StatusNotFound))
		Expect(decodeDetail(recorder.Body)).To(Equal("Unknown vault: Nope"))
	})

	It("maps a topic-not-found to 404", func() {
		fake.err = board.TopicNotFoundError{TopicID: "Missing"}
		recorder := doGet(router, "/api/topics/Missing?vault=personal")
		Expect(recorder.Code).To(Equal(http.StatusNotFound))
		Expect(decodeDetail(recorder.Body)).To(Equal("Topic not found: Missing"))
	})

	It("maps any other board failure to 500", func() {
		fake.err = errors.New("storage exploded")
		recorder := doGet(router, "/api/tasks")
		Expect(recorder.Code).To(Equal(http.StatusInternalServerError))
		Expect(decodeDetail(recorder.Body)).To(Equal("storage exploded"))
	})

	It("answers an unknown route with the FastAPI 404 body", func() {
		recorder := doGet(router, "/api/nope")
		Expect(recorder.Code).To(Equal(http.StatusNotFound))
		Expect(recorder.Body.String()).To(Equal(`{"detail":"Not Found"}`))
	})

	It("passes the topic id from the path", func() {
		doGet(router, "/api/topics/SomeTopic?vault=personal")
		Expect(fake.gotTopicID).To(Equal("SomeTopic"))
		Expect(fake.gotTopicVault).To(Equal("personal"))
	})
})

var _ = Describe("Static serving", func() {
	var router http.Handler

	BeforeEach(func() {
		router = newRouter(&fakeBoard{})
	})

	DescribeTable("serves the frozen assets",
		func(target, expected string) {
			recorder := doGet(router, target)
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(Equal(expected))
		},
		Entry("index at root", "/", "<html>index</html>"),
		Entry("index explicit", "/index.html", "<html>index</html>"),
		Entry("app.js", "/app.js", "console.log('app')"),
		Entry("style.css", "/style.css", "body{}"),
	)

	It("ignores the cache-busting query string", func() {
		versioned := doGet(router, "/app.js?v=123")
		plain := doGet(router, "/app.js")
		Expect(versioned.Body.String()).To(Equal(plain.Body.String()))
	})

	DescribeTable("refuses path traversal",
		func(target string) {
			recorder := doGet(router, target)
			Expect(recorder.Code).To(Equal(http.StatusNotFound))
			Expect(recorder.Body.String()).To(Equal(`{"detail":"Not Found"}`))
		},
		Entry("parent", "/../config.yaml"),
		Entry("encoded parent", "/%2e%2e/config.yaml"),
		Entry("nested parent", "/static/../../config.yaml"),
	)

	It("refuses a missing asset", func() {
		recorder := doGet(router, "/missing.txt")
		Expect(recorder.Code).To(Equal(http.StatusNotFound))
	})
})
