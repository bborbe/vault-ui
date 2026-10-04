// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// NewStaticHandler serves the frozen frontend from fsys at "/", resolving "/"
// to index.html and ignoring query strings. A request whose canonical path
// escapes the tree is refused with the framework 404 body and no file
// contents.
func NewStaticHandler(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			writeNotFound(resp)
			return
		}

		name, ok := canonicalName(req.URL.Path)
		if !ok {
			writeNotFound(resp)
			return
		}

		content, err := fs.ReadFile(fsys, name)
		if err != nil {
			writeNotFound(resp)
			return
		}
		resp.Header().Set("Content-Type", contentTypeFor(name))
		resp.WriteHeader(http.StatusOK)
		_, _ = resp.Write(content)
	})
}

// canonicalName cleans the request path and explicitly asserts the result stays
// inside the tree: the canonical name must be a valid fs path (no "..", no
// empty or absolute segments). Query strings are already excluded from
// URL.Path. Returns false for a path that must not be served.
func canonicalName(requestPath string) (string, bool) {
	cleaned := path.Clean("/" + strings.TrimPrefix(requestPath, "/"))
	name := strings.TrimPrefix(cleaned, "/")
	if name == "" || name == "." {
		return "index.html", true
	}
	if !fs.ValidPath(name) {
		return "", false
	}
	return name, true
}

// contentTypeFor returns the content type for a static asset.
func contentTypeFor(name string) string {
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
