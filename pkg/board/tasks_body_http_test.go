// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/bborbe/vault-cli/pkg/ops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/board"
	"github.com/bborbe/vault-ui/pkg/handler"
)

// The body cache and the handler's negotiation are proven through the real
// board and the real handler together, not a fake: a wiring mistake between the
// two layers — the board never consulted, the handler never negotiating — would
// still pass a fake-based test.
var _ = Describe("Task-list body over the real board and handler", func() {
	var h *harness
	var router http.Handler

	BeforeEach(func() {
		h = newHarness(
			item("Keep", func(i *ops.TaskListItem) { i.Status = "todo" }),
			item("Done", func(i *ops.TaskListItem) { i.Status = "completed" }),
		)
		router = handler.NewTasksHandler(h.board)
	})

	// serve issues one GET /api/tasks with the given Accept-Encoding. An empty
	// value sets no header at all.
	serve := func(acceptEncoding string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
		if acceptEncoding != "" {
			req.Header.Set("Accept-Encoding", acceptEncoding)
		}
		router.ServeHTTP(recorder, req)
		return recorder
	}

	It("serves the gzip form when the request negotiates gzip", func() {
		response := serve("gzip")
		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Header().Get("Content-Encoding")).To(Equal("gzip"))
		Expect(response.Header().Get("Vary")).To(Equal("Accept-Encoding"))
	})

	It("serves the identity form when the request negotiates nothing", func() {
		response := serve("")
		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Header().Get("Content-Encoding")).To(BeEmpty())
		Expect(response.Header().Get("Vary")).To(Equal("Accept-Encoding"))
	})

	It("decodes the gzipped form to the identity form byte for byte", func() {
		gzipped := serve("gzip")
		identity := serve("")

		reader, err := gzip.NewReader(gzipped.Body)
		Expect(err).NotTo(HaveOccurred())
		decoded, err := io.ReadAll(reader)
		Expect(err).NotTo(HaveOccurred())
		Expect(reader.Close()).To(Succeed())

		Expect(decoded).To(Equal(identity.Body.Bytes()))
	})

	It("serves a body that decodes to the board's own projection", func() {
		identity := serve("")

		var decoded []api.TaskResponse
		Expect(json.Unmarshal(identity.Body.Bytes(), &decoded)).To(Succeed())

		expected, err := h.board.ListTasks(
			context.Background(),
			board.TaskQuery{UpcomingHours: board.DefaultUpcomingHours},
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded).To(HaveLen(len(expected)))
		// Priority is an `any` that the board fills with an int and JSON
		// decodes back as a float64, so only the identity fields are compared
		// field by field rather than the whole struct with Equal.
		for i := range expected {
			Expect(decoded[i].ID).To(Equal(expected[i].ID))
			Expect(decoded[i].Status).To(Equal(expected[i].Status))
		}
	})

	It("reuses the held body for two identical requests", func() {
		first := serve("gzip")
		second := serve("gzip")

		Expect(first.Code).To(Equal(second.Code))
		Expect(first.Header()).To(Equal(second.Header()))
		Expect(first.Body.Bytes()).To(Equal(second.Body.Bytes()))
	})
})
