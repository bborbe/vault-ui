// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler_test

import (
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/handler"
)

var _ = Describe("HealthzHandler", func() {
	It("returns HTTP 200", func() {
		resp := httptest.NewRecorder()
		handler.NewHealthzHandler().ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		Expect(resp.Code).To(Equal(http.StatusOK))
	})
})

var _ = Describe("ReadinessHandler", func() {
	var readiness vaultui.Readiness

	BeforeEach(func() {
		readiness = vaultui.NewReadiness()
	})

	It("returns HTTP 503 before the service is ready", func() {
		resp := httptest.NewRecorder()
		handler.NewReadinessHandler(readiness).ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/readiness", nil))
		Expect(resp.Code).To(Equal(http.StatusServiceUnavailable))
	})

	It("returns HTTP 200 once the service is ready", func() {
		readiness.SetReady()
		resp := httptest.NewRecorder()
		handler.NewReadinessHandler(readiness).ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/readiness", nil))
		Expect(resp.Code).To(Equal(http.StatusOK))
	})
})
