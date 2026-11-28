// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"io/fs"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/bborbe/vault-ui/pkg/board"
	"github.com/bborbe/vault-ui/pkg/events"
	"github.com/bborbe/vault-ui/pkg/mutations"
	"github.com/bborbe/vault-ui/pkg/pageview"
)

// CreateHTTPRouter builds the :8000 API router: the read routes under /api/,
// the mutating routes under /api/, the Server-Sent Events stream at
// /api/events, then the static tree at /. Path cleaning is disabled so a
// traversal request reaches the static handler and is refused with the same 404
// the Python backend returns, instead of a mux 301 redirect.
func CreateHTTPRouter(
	b board.Board,
	m mutations.Service,
	staticFS fs.FS,
	hub *events.Hub,
	pages pageview.Service,
) http.Handler {
	router := mux.NewRouter()
	router.SkipClean(true)

	router.Methods(http.MethodGet).Path(vaultsRoutePath()).Handler(NewVaultsHandler(b))
	router.Methods(http.MethodGet).Path("/api/assignees").Handler(NewAssigneesHandler(b))
	router.Methods(http.MethodGet).Path("/api/source-status").Handler(NewSourceStatusHandler(b))
	router.Methods(http.MethodGet).Path("/api/tasks").Handler(NewTasksHandler(b))
	router.Methods(http.MethodGet).Path("/api/goals").Handler(NewGoalsHandler(b))
	router.Methods(http.MethodGet).Path("/api/topics").Handler(NewTopicsHandler(b))
	router.Methods(http.MethodGet).Path("/api/topics/{topic_id}").Handler(NewTopicDetailHandler(b))

	router.Methods(http.MethodPost).Path("/api/tasks/{task_id}/run").
		Handler(NewRunTaskHandler(m))
	router.Methods(http.MethodPost).Path("/api/tasks/{task_id}/jump").
		Handler(NewJumpTaskHandler(m))
	router.Methods(http.MethodPost).Path("/api/tasks/{task_id}/take-over").
		Handler(NewTakeOverTaskHandler(m))
	router.Methods(http.MethodGet).Path("/api/tasks/{task_id}/resume-command").
		Handler(NewResumeTaskCommandHandler(m))
	router.Methods(http.MethodPost).Path("/api/goals/{goal_id}/run").
		Handler(NewRunGoalHandler(m))
	router.Methods(http.MethodPost).Path("/api/goals/{goal_id}/jump").
		Handler(NewJumpGoalHandler(m))
	router.Methods(http.MethodPost).Path("/api/goals/{goal_id}/take-over").
		Handler(NewTakeOverGoalHandler(m))
	router.Methods(http.MethodPost).Path("/api/topics/{topic_id}/run").
		Handler(NewRunTopicHandler(m))
	router.Methods(http.MethodPost).Path("/api/topics/{topic_id}/jump").
		Handler(NewJumpTopicHandler(m))
	router.Methods(http.MethodPost).Path("/api/topics/{topic_id}/take-over").
		Handler(NewTakeOverTopicHandler(m))
	router.Methods(http.MethodPost).Path("/api/tasks/{task_id}/execute-command").
		Handler(NewExecuteTaskCommandHandler(m))
	router.Methods(http.MethodPatch).Path("/api/tasks/{task_id}/assign-to-me").
		Handler(NewAssignTaskHandler(m))
	router.Methods(http.MethodPatch).Path("/api/tasks/{task_id}/phase").
		Handler(NewTaskPhaseHandler(m))
	router.Methods(http.MethodPatch).Path("/api/tasks/{task_id}/flag").
		Handler(NewFlagHandler(m, "task"))
	router.Methods(http.MethodPatch).Path("/api/goals/{goal_id}/flag").
		Handler(NewFlagHandler(m, "goal"))
	router.Methods(http.MethodPatch).Path("/api/topics/{topic_id}/flag").
		Handler(NewFlagHandler(m, "topic"))
	router.Methods(http.MethodPatch).Path("/api/goals/{goal_id}/status").
		Handler(NewGoalStatusHandler(m))
	router.Methods(http.MethodPost).Path("/api/goals/{goal_id}/execute-command").
		Handler(NewExecuteGoalCommandHandler(m))
	router.Methods(http.MethodPatch).Path("/api/tasks/{task_id}/status").
		Handler(NewTaskStatusHandler(m))
	router.Methods(http.MethodPatch).Path("/api/goals/{goal_id}/assign-to-me").
		Handler(NewAssignGoalHandler(m))
	router.Methods(http.MethodDelete).Path("/api/tasks/{task_id}/session").
		Handler(NewClearTaskSessionHandler(m))
	router.Methods(http.MethodDelete).Path("/api/goals/{goal_id}/session").
		Handler(NewClearGoalSessionHandler(m))
	router.Methods(http.MethodPatch).Path("/api/tasks/{task_id}/session").
		Handler(NewSetTaskSessionHandler(m))
	router.Methods(http.MethodPost).Path("/api/config/reload").Handler(NewConfigReloadHandler(m))

	router.Methods(http.MethodGet).Path("/api/events").
		Handler(NewEventsHandler(hub, DefaultEventsHeartbeat))

	// The page routes are registered without a method restriction so a non-GET
	// request is refused by the handler with 405 rather than swallowed by the
	// catch-all below, which matches every path and verb.
	router.Path("/v/{vault}/page/{path:.+}").Handler(NewPageHandler(pages))
	router.Path("/open").Handler(NewOpenHandler(pages))

	router.PathPrefix("/").Handler(NewStaticHandler(staticFS))
	return router
}
