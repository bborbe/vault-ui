// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"net/http"

	vaultui "github.com/bborbe/vault-ui/pkg"
)

// NewReadinessHandler returns HTTP 200 once readiness.IsReady is true and
// HTTP 503 before.
func NewReadinessHandler(readiness vaultui.Readiness) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, _ *http.Request) {
		if !readiness.IsReady() {
			resp.WriteHeader(http.StatusServiceUnavailable)
			_, _ = resp.Write([]byte("not ready"))
			return
		}
		resp.WriteHeader(http.StatusOK)
		_, _ = resp.Write([]byte("OK"))
	})
}
