// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"net/http"
	"strings"

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
		body, err := b.ListTasksBody(req.Context(), board.TaskQuery{
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
		writeTaskBody(resp, req.Header.Get("Accept-Encoding"), body)
	})
}

// writeTaskBody serves a rendered task-list body, choosing the gzip form when
// the request negotiates it. Vary is set on every response — gzip and identity
// alike — so a shared cache keys on the negotiation and never serves the wrong
// form.
func writeTaskBody(resp http.ResponseWriter, acceptEncoding string, body board.TaskListBody) {
	resp.Header().Set("Content-Type", "application/json")
	resp.Header().Set("Vary", "Accept-Encoding")
	if acceptsGzip(acceptEncoding) {
		resp.Header().Set("Content-Encoding", "gzip")
		resp.WriteHeader(http.StatusOK)
		_, _ = resp.Write(body.Gzipped)
		return
	}
	resp.WriteHeader(http.StatusOK)
	_, _ = resp.Write(body.Identity)
}

// acceptsGzip reports whether the Accept-Encoding header names the gzip token.
// Only that single token is honoured; any other encoding token, an absent header
// or an unparseable value falls to the identity form. Query values (`;q=`) are
// not parsed.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		token := strings.TrimSpace(part)
		if i := strings.IndexByte(token, ';'); i >= 0 {
			token = strings.TrimSpace(token[:i])
		}
		if strings.EqualFold(token, "gzip") {
			return true
		}
	}
	return false
}

// first returns the first value, or "" when empty.
func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
