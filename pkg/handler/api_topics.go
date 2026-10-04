// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"net/http"

	"github.com/gorilla/mux"

	"github.com/bborbe/vault-ui/pkg/board"
)

// NewTopicsHandler returns the GET /api/topics handler.
func NewTopicsHandler(b board.Board) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		result, err := b.ListTopics(req.Context(), req.URL.Query()["vault"])
		if err != nil {
			writeBoardError(resp, err)
			return
		}
		writeJSON(resp, http.StatusOK, result)
	})
}

// NewTopicDetailHandler returns the GET /api/topics/{topic_id} handler.
func NewTopicDetailHandler(b board.Board) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		query := req.URL.Query()
		if !query.Has("vault") {
			writeValidation(resp, []validationItem{missingQueryError("vault")})
			return
		}
		topicID := mux.Vars(req)["topic_id"]
		result, err := b.ShowTopic(req.Context(), query.Get("vault"), topicID)
		if err != nil {
			writeBoardError(resp, err)
			return
		}
		writeJSON(resp, http.StatusOK, result)
	})
}
