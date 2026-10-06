// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	stderrors "errors"

	"github.com/gorilla/mux"

	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/mutations"
)

// statusEnum is the canonical status allowlist (UpdateStatusRequest). The order
// and text are the contract of the FastAPI 422 body.
var statusEnum = []string{"next", "in_progress", "backlog", "completed", "hold", "aborted"}

// requireVault reads the required `vault` query parameter, writing the FastAPI
// 422 body when it is absent.
func requireVault(resp http.ResponseWriter, req *http.Request) (string, bool) {
	if !req.URL.Query().Has("vault") {
		writeValidation(resp, []validationItem{missingQueryError("vault")})
		return "", false
	}
	return req.URL.Query().Get("vault"), true
}

// decodeBody decodes the JSON request body, writing the FastAPI 422 body on a
// malformed payload.
func decodeBody(resp http.ResponseWriter, req *http.Request, dst any) bool {
	decoder := json.NewDecoder(req.Body)
	if err := decoder.Decode(dst); err != nil {
		writeValidation(resp, []validationItem{{
			Type: "json_invalid",
			Loc:  []string{"body"},
			Msg:  "JSON decode error",
		}})
		return false
	}
	return true
}

// writeMutationError maps a mutation error to the Python status/body contract.
func writeMutationError(resp http.ResponseWriter, err error) {
	var httpErr *mutations.HTTPError
	if stderrors.As(err, &httpErr) {
		writeDetail(resp, httpErr.Status, httpErr.Detail)
		return
	}
	writeDetail(resp, http.StatusInternalServerError, err.Error())
}

// pathVar returns the named mux path variable.
func pathVar(req *http.Request, name string) string {
	return mux.Vars(req)[name]
}

// sameOrigin reproduces _is_same_origin for the jump route.
func sameOrigin(req *http.Request) bool {
	host := req.Host
	if host == "" {
		return false
	}
	origin := req.Header.Get("Origin")
	if origin == "" {
		referer := req.Header.Get("Referer")
		if referer == "" {
			return true
		}
		parts, err := url.Parse(referer)
		if err != nil || parts.Scheme == "" || parts.Host == "" {
			return false
		}
		origin = parts.Scheme + "://" + parts.Host
	}
	parts, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return parts.Host == host
}

// validStatus reports whether value is in the status enum.
func validStatus(value string) bool {
	for _, allowed := range statusEnum {
		if value == allowed {
			return true
		}
	}
	return false
}

// writeStatusEnumError writes the FastAPI literal_error body for a bad status.
func writeStatusEnumError(resp http.ResponseWriter, value string) {
	expected := "'" + strings.Join(statusEnum[:len(statusEnum)-1], "', '") +
		"' or '" + statusEnum[len(statusEnum)-1] + "'"
	writeValidation(resp, []validationItem{{
		Type:  "literal_error",
		Loc:   []string{"body", "status"},
		Msg:   "Input should be " + expected,
		Input: value,
		Ctx:   map[string]any{"expected": expected},
	}})
}

// NewRunTaskHandler returns the POST /api/tasks/{task_id}/run handler.
func NewRunTaskHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		vault, ok := requireVault(resp, req)
		if !ok {
			return
		}
		result, err := m.RunTask(req.Context(), vault, pathVar(req, "task_id"))
		if err != nil {
			writeMutationError(resp, err)
			return
		}
		writeJSON(resp, http.StatusOK, result)
	})
}

// NewJumpTaskHandler returns the POST /api/tasks/{task_id}/jump handler.
func NewJumpTaskHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		vault, ok := requireVault(resp, req)
		if !ok {
			return
		}
		if err := m.JumpTask(
			req.Context(), vault, pathVar(req, "task_id"), sameOrigin(req),
		); err != nil {
			writeMutationError(resp, err)
			return
		}
		resp.WriteHeader(http.StatusNoContent)
	})
}

// NewTakeOverTaskHandler returns the POST /api/tasks/{task_id}/take-over handler.
func NewTakeOverTaskHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		vault, ok := requireVault(resp, req)
		if !ok {
			return
		}
		result, err := m.TakeOverTask(req.Context(), vault, pathVar(req, "task_id"))
		if err != nil {
			writeMutationError(resp, err)
			return
		}
		writeJSON(resp, http.StatusOK, result)
	})
}

// NewRunGoalHandler returns the POST /api/goals/{goal_id}/run handler.
func NewRunGoalHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		vault, ok := requireVault(resp, req)
		if !ok {
			return
		}
		result, err := m.RunGoal(req.Context(), vault, pathVar(req, "goal_id"))
		if err != nil {
			writeMutationError(resp, err)
			return
		}
		writeJSON(resp, http.StatusOK, result)
	})
}

// NewTakeOverGoalHandler returns the POST /api/goals/{goal_id}/take-over handler.
func NewTakeOverGoalHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		vault, ok := requireVault(resp, req)
		if !ok {
			return
		}
		result, err := m.TakeOverGoal(req.Context(), vault, pathVar(req, "goal_id"))
		if err != nil {
			writeMutationError(resp, err)
			return
		}
		writeJSON(resp, http.StatusOK, result)
	})
}

// NewExecuteTaskCommandHandler returns the POST /api/tasks/{task_id}/execute-command handler.
func NewExecuteTaskCommandHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		vault, ok := requireVault(resp, req)
		if !ok {
			return
		}
		var body api.ExecuteCommandRequest
		if !decodeBody(resp, req, &body) {
			return
		}
		result, err := m.ExecuteTaskCommand(
			req.Context(), vault, pathVar(req, "task_id"), body,
		)
		if err != nil {
			writeMutationError(resp, err)
			return
		}
		writeJSON(resp, http.StatusOK, result)
	})
}

// NewAssignTaskHandler returns the PATCH /api/tasks/{task_id}/assign-to-me
// handler. The write is queued, so the handler answers 202 with the requested
// assignee.
func NewAssignTaskHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		vault, ok := requireVault(resp, req)
		if !ok {
			return
		}
		result, err := m.AssignTaskToMe(req.Context(), vault, pathVar(req, "task_id"))
		if err != nil {
			writeMutationError(resp, err)
			return
		}
		writeJSON(resp, http.StatusAccepted, result)
	})
}

// NewTaskPhaseHandler returns the PATCH /api/tasks/{task_id}/phase handler. The
// write is queued, so the handler answers 202 with the requested phase.
func NewTaskPhaseHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		vault, ok := requireVault(resp, req)
		if !ok {
			return
		}
		var body api.UpdatePhaseRequest
		if !decodeBody(resp, req, &body) {
			return
		}
		result, err := m.UpdateTaskPhase(req.Context(), vault, pathVar(req, "task_id"), body)
		if err != nil {
			writeMutationError(resp, err)
			return
		}
		writeJSON(resp, http.StatusAccepted, result)
	})
}

// NewTaskFlagHandler returns the PATCH /api/tasks/{task_id}/flag handler. The
// write is queued, so the handler answers 202 with the requested flag value.
func NewTaskFlagHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		vault, ok := requireVault(resp, req)
		if !ok {
			return
		}
		var body api.UpdateFlagRequest
		if !decodeBody(resp, req, &body) {
			return
		}
		result, err := m.UpdateTaskFlag(req.Context(), vault, pathVar(req, "task_id"), body)
		if err != nil {
			writeMutationError(resp, err)
			return
		}
		writeJSON(resp, http.StatusAccepted, result)
	})
}

// NewTaskStatusHandler returns the PATCH /api/tasks/{task_id}/status handler.
// The write is queued, so the handler answers 202 with the requested status.
func NewTaskStatusHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		vault, ok := requireVault(resp, req)
		if !ok {
			return
		}
		var body api.UpdateStatusRequest
		if !decodeBody(resp, req, &body) {
			return
		}
		if !validStatus(body.Status) {
			writeStatusEnumError(resp, body.Status)
			return
		}
		result, err := m.UpdateTaskStatus(req.Context(), vault, pathVar(req, "task_id"), body)
		if err != nil {
			writeMutationError(resp, err)
			return
		}
		writeJSON(resp, http.StatusAccepted, result)
	})
}

// NewGoalStatusHandler returns the PATCH /api/goals/{goal_id}/status handler.
// The write is queued, so the handler answers 202 with the requested status.
func NewGoalStatusHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		vault, ok := requireVault(resp, req)
		if !ok {
			return
		}
		var body api.UpdateStatusRequest
		if !decodeBody(resp, req, &body) {
			return
		}
		if !validStatus(body.Status) {
			writeStatusEnumError(resp, body.Status)
			return
		}
		result, err := m.UpdateGoalStatus(req.Context(), vault, pathVar(req, "goal_id"), body)
		if err != nil {
			writeMutationError(resp, err)
			return
		}
		writeJSON(resp, http.StatusAccepted, result)
	})
}

// NewExecuteGoalCommandHandler returns the POST /api/goals/{goal_id}/execute-command handler.
func NewExecuteGoalCommandHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		vault, ok := requireVault(resp, req)
		if !ok {
			return
		}
		var body api.ExecuteCommandRequest
		if !decodeBody(resp, req, &body) {
			return
		}
		result, err := m.ExecuteGoalCommand(req.Context(), vault, pathVar(req, "goal_id"), body)
		if err != nil {
			writeMutationError(resp, err)
			return
		}
		writeJSON(resp, http.StatusOK, result)
	})
}

// NewAssignGoalHandler returns the PATCH /api/goals/{goal_id}/assign-to-me
// handler. The write is queued, so the handler answers 202 with the requested
// assignee.
func NewAssignGoalHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		vault, ok := requireVault(resp, req)
		if !ok {
			return
		}
		result, err := m.AssignGoalToMe(req.Context(), vault, pathVar(req, "goal_id"))
		if err != nil {
			writeMutationError(resp, err)
			return
		}
		writeJSON(resp, http.StatusAccepted, result)
	})
}

// NewClearTaskSessionHandler returns the DELETE /api/tasks/{task_id}/session
// handler. The write is queued, so the handler answers 202 with the requested
// session clear.
func NewClearTaskSessionHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		vault, ok := requireVault(resp, req)
		if !ok {
			return
		}
		result, err := m.ClearTaskSession(req.Context(), vault, pathVar(req, "task_id"))
		if err != nil {
			writeMutationError(resp, err)
			return
		}
		writeJSON(resp, http.StatusAccepted, result)
	})
}

// NewClearGoalSessionHandler returns the DELETE /api/goals/{goal_id}/session
// handler. The write is queued, so the handler answers 202 with the requested
// session clear.
func NewClearGoalSessionHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		vault, ok := requireVault(resp, req)
		if !ok {
			return
		}
		result, err := m.ClearGoalSession(req.Context(), vault, pathVar(req, "goal_id"))
		if err != nil {
			writeMutationError(resp, err)
			return
		}
		writeJSON(resp, http.StatusAccepted, result)
	})
}

// NewSetTaskSessionHandler returns the PATCH /api/tasks/{task_id}/session
// handler. The write is queued, so the handler answers 202 with the requested
// session id.
func NewSetTaskSessionHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		vault, ok := requireVault(resp, req)
		if !ok {
			return
		}
		var body api.UpdateSessionRequest
		if !decodeBody(resp, req, &body) {
			return
		}
		result, err := m.SetTaskSession(req.Context(), vault, pathVar(req, "task_id"), body)
		if err != nil {
			writeMutationError(resp, err)
			return
		}
		writeJSON(resp, http.StatusAccepted, result)
	})
}

// NewCacheReloadHandler returns the POST /api/cache/reload handler.
func NewCacheReloadHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		result, err := m.ReloadCache(req.Context(), req.URL.Query().Get("vault"))
		if err != nil {
			writeMutationError(resp, err)
			return
		}
		writeJSON(resp, http.StatusOK, result)
	})
}

// NewConfigReloadHandler returns the POST /api/config/reload handler.
func NewConfigReloadHandler(m mutations.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		result, err := m.ReloadConfig(req.Context())
		if err != nil {
			writeMutationError(resp, err)
			return
		}
		writeJSON(resp, http.StatusOK, result)
	})
}
