// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"net/http"

	"github.com/bborbe/vault-ui/pkg/board"
)

// NewTasksHandler returns the GET /api/tasks handler.
func NewTasksHandler(b board.Board) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		query := req.URL.Query()
		hours, invalid := parseUpcomingHours(query)
		if invalid != nil {
			writeValidation(resp, []validationItem{*invalid})
			return
		}
		result, err := b.ListTasks(req.Context(), board.TaskQuery{
			Vaults:        query["vault"],
			Statuses:      query["status"],
			Phases:        query["phase"],
			Assignees:     query["assignee"],
			Goals:         query["goal"],
			UpcomingHours: hours,
			SessionLive:   parseBool(first(query["session_live"])),
		})
		if err != nil {
			writeBoardError(resp, err)
			return
		}
		writeJSON(resp, http.StatusOK, result)
	})
}

// first returns the first value, or "" when empty.
func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
