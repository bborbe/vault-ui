// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"io/fs"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/bborbe/vault-ui/pkg/board"
)

// CreateHTTPRouter builds the :8000 API router: the read routes under /api/,
// then the static tree at /. Path cleaning is disabled so a traversal request
// reaches the static handler and is refused with the same 404 the Python
// backend returns, instead of a mux 301 redirect.
func CreateHTTPRouter(b board.Board, staticFS fs.FS) http.Handler {
	router := mux.NewRouter()
	router.SkipClean(true)

	router.Methods(http.MethodGet).Path(vaultsRoutePath()).Handler(NewVaultsHandler(b))
	router.Methods(http.MethodGet).Path("/api/assignees").Handler(NewAssigneesHandler(b))
	router.Methods(http.MethodGet).Path("/api/tasks").Handler(NewTasksHandler(b))
	router.Methods(http.MethodGet).Path("/api/goals").Handler(NewGoalsHandler(b))
	router.Methods(http.MethodGet).Path("/api/topics").Handler(NewTopicsHandler(b))
	router.Methods(http.MethodGet).Path("/api/topics/{topic_id}").Handler(NewTopicDetailHandler(b))

	router.PathPrefix("/").Handler(NewStaticHandler(staticFS))
	return router
}
