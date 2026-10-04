// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"net/http"

	"github.com/bborbe/vault-ui/pkg/board"
)

// NewVaultsHandler returns the GET /api/vaults handler.
func NewVaultsHandler(b board.Board) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		result, err := b.ListVaults(req.Context())
		if err != nil {
			writeBoardError(resp, err)
			return
		}
		if selftestDivergeVaultsResponse(resp, result) {
			return
		}
		writeJSON(resp, http.StatusOK, result)
	})
}

// NewAssigneesHandler returns the GET /api/assignees handler.
func NewAssigneesHandler(b board.Board) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		result, err := b.ListAssignees(req.Context(), req.URL.Query()["vault"])
		if err != nil {
			writeBoardError(resp, err)
			return
		}
		writeJSON(resp, http.StatusOK, result)
	})
}
