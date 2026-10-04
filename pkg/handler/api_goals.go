// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"net/http"

	"github.com/bborbe/vault-ui/pkg/board"
)

// NewGoalsHandler returns the GET /api/goals handler.
func NewGoalsHandler(b board.Board) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		query := req.URL.Query()
		hours, invalid := parseUpcomingHours(query)
		if invalid != nil {
			writeValidation(resp, []validationItem{*invalid})
			return
		}
		result, err := b.ListGoals(req.Context(), board.GoalQuery{
			Vaults:        query["vault"],
			Statuses:      query["status"],
			Assignees:     query["assignee"],
			UpcomingHours: hours,
		})
		if err != nil {
			writeBoardError(resp, err)
			return
		}
		writeJSON(resp, http.StatusOK, result)
	})
}
