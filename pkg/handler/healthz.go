// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"net/http"

	libhttp "github.com/bborbe/http"
)

// NewHealthzHandler returns the canonical liveness handler: always HTTP 200.
func NewHealthzHandler() http.Handler {
	return libhttp.NewPrintHandler("OK")
}
