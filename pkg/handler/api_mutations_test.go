// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/handler"
	"github.com/bborbe/vault-ui/pkg/mutations"
)

// doRequest issues a request against a single handler with the given body.
func doRequest(
	handler http.Handler, method, target, body string,
) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	return recorder
}

var _ = Describe("Mutation handlers", func() {
	Describe("required vault query parameter", func() {
		DescribeTable("422s every task and goal route without vault",
			func(handler http.Handler, method, target string) {
				recorder := doRequest(handler, method, target, "{}")
				Expect(recorder.Code).To(Equal(http.StatusUnprocessableEntity))
				Expect(recorder.Body.String()).To(ContainSubstring(`["query","vault"]`))
			},
			Entry("run task", handler.NewRunTaskHandler(&fakeMutations{}),
				http.MethodPost, "/api/tasks/TaskOne/run"),
			Entry("jump task", handler.NewJumpTaskHandler(&fakeMutations{}),
				http.MethodPost, "/api/tasks/TaskOne/jump"),
			Entry("take-over task", handler.NewTakeOverTaskHandler(&fakeMutations{}),
				http.MethodPost, "/api/tasks/TaskOne/take-over"),
			Entry("run goal", handler.NewRunGoalHandler(&fakeMutations{}),
				http.MethodPost, "/api/goals/GoalOne/run"),
			Entry("take-over goal", handler.NewTakeOverGoalHandler(&fakeMutations{}),
				http.MethodPost, "/api/goals/GoalOne/take-over"),
			Entry("execute task command", handler.NewExecuteTaskCommandHandler(&fakeMutations{}),
				http.MethodPost, "/api/tasks/TaskOne/execute-command"),
			Entry("assign task", handler.NewAssignTaskHandler(&fakeMutations{}),
				http.MethodPatch, "/api/tasks/TaskOne/assign-to-me"),
			Entry("task phase", handler.NewTaskPhaseHandler(&fakeMutations{}),
				http.MethodPatch, "/api/tasks/TaskOne/phase"),
			Entry("task flag", handler.NewTaskFlagHandler(&fakeMutations{}),
				http.MethodPatch, "/api/tasks/TaskOne/flag"),
			Entry("task status", handler.NewTaskStatusHandler(&fakeMutations{}),
				http.MethodPatch, "/api/tasks/TaskOne/status"),
			Entry("goal status", handler.NewGoalStatusHandler(&fakeMutations{}),
				http.MethodPatch, "/api/goals/GoalOne/status"),
			Entry("goal execute command", handler.NewExecuteGoalCommandHandler(&fakeMutations{}),
				http.MethodPost, "/api/goals/GoalOne/execute-command"),
			Entry("assign goal", handler.NewAssignGoalHandler(&fakeMutations{}),
				http.MethodPatch, "/api/goals/GoalOne/assign-to-me"),
			Entry("clear task session", handler.NewClearTaskSessionHandler(&fakeMutations{}),
				http.MethodDelete, "/api/tasks/TaskOne/session"),
			Entry("clear goal session", handler.NewClearGoalSessionHandler(&fakeMutations{}),
				http.MethodDelete, "/api/goals/GoalOne/session"),
			Entry("set task session", handler.NewSetTaskSessionHandler(&fakeMutations{}),
				http.MethodPatch, "/api/tasks/TaskOne/session"),
		)
	})

	It("does not require vault on the maintenance routes", func() {
		recorder := doRequest(
			handler.NewCacheReloadHandler(&fakeMutations{
				reloadCache: func(context.Context, string) (api.CacheReloadResponse, error) {
					return api.CacheReloadResponse{Reloaded: []string{}, Counts: map[string]int{}}, nil
				},
			}), http.MethodPost, "/api/cache/reload", "",
		)
		Expect(recorder.Code).To(Equal(http.StatusOK))
	})

	It("422s an out-of-enum status before any vault operation", func() {
		called := false
		recorder := doRequest(
			handler.NewTaskStatusHandler(&fakeMutations{
				updateTaskStatus: func(
					context.Context, string, string, api.UpdateStatusRequest,
				) (api.StatusUpdateResponse, error) {
					called = true
					return api.StatusUpdateResponse{}, nil
				},
			}), http.MethodPatch, "/api/tasks/TaskOne/status?vault=personal",
			`{"status":"bogus"}`,
		)
		Expect(recorder.Code).To(Equal(http.StatusUnprocessableEntity))
		Expect(recorder.Body.String()).To(ContainSubstring("literal_error"))
		Expect(called).To(BeFalse())
	})

	It("propagates a mutation HTTPError verbatim", func() {
		recorder := doRequest(
			handler.NewTaskStatusHandler(&fakeMutations{
				updateTaskStatus: func(
					context.Context, string, string, api.UpdateStatusRequest,
				) (api.StatusUpdateResponse, error) {
					return api.StatusUpdateResponse{}, &mutations.HTTPError{
						Status: http.StatusBadRequest, Detail: "task_id must not start with '-'",
					}
				},
			}), http.MethodPatch, "/api/tasks/-bad/status?vault=personal",
			`{"status":"backlog"}`,
		)
		Expect(recorder.Code).To(Equal(http.StatusBadRequest))
		Expect(recorder.Body.String()).To(ContainSubstring("must not start with"))
	})

	It("204s a successful jump", func() {
		recorder := doRequest(
			handler.NewJumpTaskHandler(&fakeMutations{
				jumpTask: func(_ context.Context, _, _ string, _ bool) error { return nil },
			}), http.MethodPost, "/api/tasks/TaskOne/jump?vault=personal", "",
		)
		Expect(recorder.Code).To(Equal(http.StatusNoContent))
		Expect(recorder.Body.String()).To(BeEmpty())
	})

	It("passes the same-origin verdict to the jump service", func() {
		var gotSameOrigin bool
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(
			http.MethodPost, "/api/tasks/TaskOne/jump?vault=personal", nil,
		)
		request.Header.Set("Origin", "http://evil.example")
		handler.NewJumpTaskHandler(&fakeMutations{
			jumpTask: func(_ context.Context, _, _ string, sameOrigin bool) error {
				gotSameOrigin = sameOrigin
				return &mutations.HTTPError{Status: http.StatusForbidden, Detail: "cross-origin"}
			},
		}).ServeHTTP(recorder, request)
		Expect(gotSameOrigin).To(BeFalse())
		Expect(recorder.Code).To(Equal(http.StatusForbidden))
	})

	It("writes the flag body", func() {
		recorder := doRequest(
			handler.NewTaskFlagHandler(&fakeMutations{
				updateTaskFlag: func(
					_ context.Context, _, _ string, req api.UpdateFlagRequest,
				) (api.FlagUpdateResponse, error) {
					return api.FlagUpdateResponse{Status: "success", TaskID: "TaskOne", Flag: true}, nil
				},
			}), http.MethodPatch, "/api/tasks/TaskOne/flag?vault=personal", `{}`,
		)
		Expect(recorder.Code).To(Equal(http.StatusOK))
		Expect(recorder.Body.String()).To(ContainSubstring(`"flag":true`))
	})

	It("writes the assign-to-me body", func() {
		recorder := doRequest(
			handler.NewAssignTaskHandler(&fakeMutations{
				assignTaskToMe: func(_ context.Context, _, _ string) (api.AssignResponse, error) {
					return api.AssignResponse{
						Status: "success", TaskID: "TaskOne", Assignee: "fixtureuser",
					}, nil
				},
			}), http.MethodPatch, "/api/tasks/TaskOne/assign-to-me?vault=personal", "",
		)
		Expect(recorder.Code).To(Equal(http.StatusOK))
		Expect(recorder.Body.String()).To(ContainSubstring(`"assignee":"fixtureuser"`))
	})

	It("writes the session-clear body", func() {
		recorder := doRequest(
			handler.NewClearTaskSessionHandler(&fakeMutations{
				clearTaskSession: func(
					_ context.Context, _, _ string,
				) (api.SessionClearResponse, error) {
					return api.SessionClearResponse{Status: "success", TaskID: "TaskOne"}, nil
				},
			}), http.MethodDelete, "/api/tasks/TaskOne/session?vault=personal", "",
		)
		Expect(recorder.Code).To(Equal(http.StatusOK))
		Expect(recorder.Body.String()).To(ContainSubstring(`"status":"success"`))
	})

	It("writes the config-reload body", func() {
		recorder := doRequest(
			handler.NewConfigReloadHandler(&fakeMutations{
				reloadConfig: func(context.Context) (api.ConfigReloadResponse, error) {
					return api.ConfigReloadResponse{
						Vaults: []string{"personal"}, Watchers: []string{"personal"},
					}, nil
				},
			}), http.MethodPost, "/api/config/reload", "",
		)
		Expect(recorder.Code).To(Equal(http.StatusOK))
		Expect(recorder.Body.String()).To(ContainSubstring(`"watchers":["personal"]`))
	})
})

var _ = Describe("Mutation handler success paths", func() {
	emptySession := api.SessionResponse{
		SessionID: "s", Command: "c", WorkingDir: "w", TaskTitle: "t",
	}
	DescribeTable("200s the mutating routes",
		func(handler http.Handler, method, target, body string, want int) {
			recorder := doRequest(handler, method, target, body)
			Expect(recorder.Code).To(Equal(want))
		},
		Entry("run task", handler.NewRunTaskHandler(&fakeMutations{
			runTask: func(context.Context, string, string) (api.SessionResponse, error) {
				return emptySession, nil
			},
		}), http.MethodPost, "/api/tasks/TaskOne/run?vault=personal", "", http.StatusOK),
		Entry("take-over task", handler.NewTakeOverTaskHandler(&fakeMutations{
			takeOverTask: func(context.Context, string, string) (api.SessionResponse, error) {
				return emptySession, nil
			},
		}), http.MethodPost, "/api/tasks/TaskOne/take-over?vault=personal", "", http.StatusOK),
		Entry("run goal", handler.NewRunGoalHandler(&fakeMutations{
			runGoal: func(context.Context, string, string) (api.SessionResponse, error) {
				return emptySession, nil
			},
		}), http.MethodPost, "/api/goals/GoalOne/run?vault=personal", "", http.StatusOK),
		Entry("take-over goal", handler.NewTakeOverGoalHandler(&fakeMutations{
			takeOverGoal: func(context.Context, string, string) (api.SessionResponse, error) {
				return emptySession, nil
			},
		}), http.MethodPost, "/api/goals/GoalOne/take-over?vault=personal", "", http.StatusOK),
		Entry("execute task command", handler.NewExecuteTaskCommandHandler(&fakeMutations{
			executeTaskCommand: func(
				context.Context, string, string, api.ExecuteCommandRequest,
			) (api.SessionResponse, error) {
				return emptySession, nil
			},
		}), http.MethodPost, "/api/tasks/TaskOne/execute-command?vault=personal",
			`{"command":"complete-task"}`, http.StatusOK),
		Entry("task phase", handler.NewTaskPhaseHandler(&fakeMutations{
			updateTaskPhase: func(
				context.Context, string, string, api.UpdatePhaseRequest,
			) (api.PhaseUpdateResponse, error) {
				return api.PhaseUpdateResponse{Status: "success"}, nil
			},
		}), http.MethodPatch, "/api/tasks/TaskOne/phase?vault=personal",
			`{"phase":"execution"}`, http.StatusOK),
		Entry("goal status", handler.NewGoalStatusHandler(&fakeMutations{
			updateGoalStatus: func(
				context.Context, string, string, api.UpdateStatusRequest,
			) (api.StatusUpdateResponse, error) {
				return api.StatusUpdateResponse{Status: "success"}, nil
			},
		}), http.MethodPatch, "/api/goals/GoalOne/status?vault=personal",
			`{"status":"hold"}`, http.StatusOK),
		Entry("goal execute command", handler.NewExecuteGoalCommandHandler(&fakeMutations{
			executeGoalCommand: func(
				context.Context, string, string, api.ExecuteCommandRequest,
			) (api.GoalCommandResponse, error) {
				return api.GoalCommandResponse{Status: "success"}, nil
			},
		}), http.MethodPost, "/api/goals/GoalOne/execute-command?vault=personal",
			`{"command":"complete-goal"}`, http.StatusOK),
		Entry("assign goal", handler.NewAssignGoalHandler(&fakeMutations{
			assignGoalToMe: func(context.Context, string, string) (api.AssignResponse, error) {
				return api.AssignResponse{Status: "success"}, nil
			},
		}), http.MethodPatch, "/api/goals/GoalOne/assign-to-me?vault=personal", "", http.StatusOK),
		Entry("clear goal session", handler.NewClearGoalSessionHandler(&fakeMutations{
			clearGoalSession: func(context.Context, string, string) (api.SessionClearResponse, error) {
				return api.SessionClearResponse{Status: "success"}, nil
			},
		}), http.MethodDelete, "/api/goals/GoalOne/session?vault=personal", "", http.StatusOK),
		Entry("set task session", handler.NewSetTaskSessionHandler(&fakeMutations{
			setTaskSession: func(
				context.Context, string, string, api.UpdateSessionRequest,
			) (api.SessionSetResponse, error) {
				return api.SessionSetResponse{Status: "success"}, nil
			},
		}), http.MethodPatch, "/api/tasks/TaskOne/session?vault=personal",
			`{"claude_session_id":"x"}`, http.StatusOK),
	)

	It("500s an unclassified service error", func() {
		recorder := doRequest(
			handler.NewTaskFlagHandler(&fakeMutations{
				updateTaskFlag: func(
					context.Context, string, string, api.UpdateFlagRequest,
				) (api.FlagUpdateResponse, error) {
					return api.FlagUpdateResponse{}, errors.New("boom")
				},
			}), http.MethodPatch, "/api/tasks/TaskOne/flag?vault=personal", `{}`,
		)
		Expect(recorder.Code).To(Equal(http.StatusInternalServerError))
	})

	It("422s a malformed body", func() {
		recorder := doRequest(
			handler.NewTaskFlagHandler(&fakeMutations{
				updateTaskFlag: func(
					context.Context, string, string, api.UpdateFlagRequest,
				) (api.FlagUpdateResponse, error) {
					return api.FlagUpdateResponse{}, nil
				},
			}), http.MethodPatch, "/api/tasks/TaskOne/flag?vault=personal", `{`,
		)
		Expect(recorder.Code).To(Equal(http.StatusUnprocessableEntity))
	})
})
