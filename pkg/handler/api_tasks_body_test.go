// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler_test

import (
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/board"
)

// doGetWithEncoding issues a GET with the given Accept-Encoding header. An
// empty value sets no header at all.
func doGetWithEncoding(router http.Handler, target, acceptEncoding string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	router.ServeHTTP(recorder, req)
	return recorder
}

var _ = Describe("Task-list body negotiation", func() {
	var fake *fakeBoard
	var router http.Handler

	BeforeEach(func() {
		fake = &fakeBoard{
			taskBody: board.TaskListBody{
				Identity: []byte("identity-form"),
				Gzipped:  []byte("gzip-form"),
			},
		}
		router = newRouter(fake)
	})

	It("serves the gzip form and its headers when gzip is negotiated", func() {
		recorder := doGetWithEncoding(router, "/api/tasks", "gzip")

		Expect(recorder.Code).To(Equal(http.StatusOK))
		Expect(recorder.Header().Get("Content-Encoding")).To(Equal("gzip"))
		Expect(recorder.Header().Get("Vary")).To(Equal("Accept-Encoding"))
		Expect(recorder.Body.String()).To(Equal("gzip-form"))
	})

	It("serves the identity form with Vary when no encoding is negotiated", func() {
		recorder := doGetWithEncoding(router, "/api/tasks", "")

		Expect(recorder.Code).To(Equal(http.StatusOK))
		Expect(recorder.Header().Get("Content-Encoding")).To(BeEmpty())
		Expect(recorder.Header().Get("Vary")).To(Equal("Accept-Encoding"))
		Expect(recorder.Body.String()).To(Equal("identity-form"))
	})

	It("does not honour another encoding token", func() {
		recorder := doGetWithEncoding(router, "/api/tasks", "deflate")

		Expect(recorder.Code).To(Equal(http.StatusOK))
		Expect(recorder.Header().Get("Content-Encoding")).To(BeEmpty())
		Expect(recorder.Body.String()).To(Equal("identity-form"))
	})

	It("honours gzip inside a token list", func() {
		recorder := doGetWithEncoding(router, "/api/tasks", "deflate, gzip")

		Expect(recorder.Code).To(Equal(http.StatusOK))
		Expect(recorder.Header().Get("Content-Encoding")).To(Equal("gzip"))
		Expect(recorder.Body.String()).To(Equal("gzip-form"))
	})
})
