// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	stderrors "errors"
	"net/http"

	"github.com/golang/glog"
	"github.com/gorilla/mux"

	"github.com/bborbe/vault-ui/pkg/pageview"
)

// NewPageHandler returns the GET /v/{vault}/page/{path} handler. It claims every
// method so a non-GET request is answered 405 here instead of falling through to
// the static catch-all, which matches every verb.
func NewPageHandler(pages pageview.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			writeDetail(resp, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		vars := mux.Vars(req)
		vault := vars["vault"]
		pagePath := vars["path"]
		doc, err := pages.Render(req.Context(), vault, pagePath)
		if err != nil {
			var nf pageview.NotFoundError
			if stderrors.As(err, &nf) {
				writeNotFound(resp)
				return
			}
			glog.Errorf("render page %s/%s: %v", vault, pagePath, err)
			writeDetail(resp, http.StatusInternalServerError, "Internal Server Error")
			return
		}
		resp.Header().Set("Content-Type", "text/html; charset=utf-8")
		resp.WriteHeader(http.StatusOK)
		_, _ = resp.Write([]byte(doc))
	})
}

// NewOpenHandler returns the GET /open handler that answers Obsidian's
// `vault`/`file` parameters with a redirect to the in-app page route. Like the
// page handler, it claims every method and refuses non-GET itself.
func NewOpenHandler(pages pageview.Service) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			writeDetail(resp, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		query := req.URL.Query()
		vault := query.Get("vault")
		file := query.Get("file")
		ok, err := pages.Exists(req.Context(), vault, file)
		if err != nil {
			var nf pageview.NotFoundError
			if stderrors.As(err, &nf) {
				writeNotFound(resp)
				return
			}
			glog.Errorf("open page %s/%s: %v", vault, file, err)
			writeDetail(resp, http.StatusInternalServerError, "Internal Server Error")
			return
		}
		if !ok {
			writeNotFound(resp)
			return
		}
		http.Redirect(resp, req, pageview.PageURL(vault, file), http.StatusFound)
	})
}
