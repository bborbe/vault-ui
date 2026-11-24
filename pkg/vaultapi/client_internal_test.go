// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Client page reads", func() {
	It("decodes a page and sends the vault name", func() {
		var gotVault, gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotVault = r.URL.Query().Get("vault")
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"vault":"private-personal",
				"path":"Alpha.md",
				"name":"Alpha",
				"content":"page body",
				"links":{"Beta":"Beta.md"},
				"embeds":{"Gamma":"gamma body"}
			}`))
		}))
		defer server.Close()

		client := New(Config{BaseURL: server.URL})
		page, err := client.Page(context.Background(), "private-personal", "Alpha.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(gotPath).To(Equal("/api/v1/pages/Alpha.md"))
		Expect(gotVault).To(Equal("private-personal"))
		Expect(page.Name).To(Equal("Alpha"))
		Expect(page.Content).To(Equal("page body"))
		Expect(page.Links).To(Equal(map[string]string{"Beta": "Beta.md"}))
		Expect(page.Embeds).To(Equal(map[string]string{"Gamma": "gamma body"}))
	})

	It("omits the vault query when the name is empty", func() {
		var rawQuery string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawQuery = r.URL.RawQuery
			_, _ = w.Write([]byte(`{"name":"Alpha"}`))
		}))
		defer server.Close()

		_, err := New(Config{BaseURL: server.URL}).Page(context.Background(), "", "Alpha.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(rawQuery).To(BeEmpty())
	})

	It("decodes backlinks and sends the vault name", func() {
		var gotVault, gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotVault = r.URL.Query().Get("vault")
			gotPath = r.URL.Path
			_, _ = w.Write([]byte(`{"vault":"private-personal","path":"Alpha.md","name":"Alpha","backlinks":[{"path":"24 Goals/Links.md","name":"Links"}]}`))
		}))
		defer server.Close()

		links, err := New(Config{BaseURL: server.URL}).Backlinks(
			context.Background(), "private-personal", "Alpha.md",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(gotPath).To(Equal("/api/v1/pages/Alpha.md/backlinks"))
		Expect(gotVault).To(Equal("private-personal"))
		Expect(links).To(Equal([]Backlink{{Path: "24 Goals/Links.md", Name: "Links"}}))
	})

	It("maps a 404 to the typed not-found", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not_found"}`))
		}))
		defer server.Close()

		_, err := New(Config{BaseURL: server.URL}).Page(context.Background(), "v", "Missing.md")
		Expect(err).To(HaveOccurred())
		var notFound NotFoundError
		Expect(stderrors.As(err, &notFound)).To(BeTrue())
	})

	It("returns a plain error for a non-404 failure", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		_, err := New(Config{BaseURL: server.URL}).Page(context.Background(), "v", "Alpha.md")
		Expect(err).To(HaveOccurred())
		var notFound NotFoundError
		Expect(stderrors.As(err, &notFound)).To(BeFalse())
	})

	It("escapes the path per segment and preserves the slashes", func() {
		var escapedPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			escapedPath = r.URL.EscapedPath()
			_, _ = w.Write([]byte(`{"name":"Some Goal"}`))
		}))
		defer server.Close()

		_, err := New(Config{BaseURL: server.URL}).Page(
			context.Background(), "v", "24 Goals/Some Goal.md",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(escapedPath).To(Equal("/api/v1/pages/24%20Goals/Some%20Goal.md"))
	})
})
