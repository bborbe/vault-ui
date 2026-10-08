// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package heartbeat_test

import (
	"context"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/heartbeat"
)

const liveID = "11111111-1111-1111-1111-111111111111"

// newStoreAt serves handler and returns a Store pointed at it.
func newStoreAt(handler http.HandlerFunc) (heartbeat.Store, func()) {
	server := httptest.NewServer(handler)
	return heartbeat.NewStore(server.URL, nil), server.Close
}

var _ = Describe("Store", func() {
	Describe("LiveSessionIDs", func() {
		It("returns only the ids the store reports live", func() {
			store, closeServer := newStoreAt(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal("/api/1.0/session-heartbeat"))
				_, _ = w.Write([]byte(`[
					{"session_id":"` + liveID + `","live":true,"age_seconds":3},
					{"session_id":"stale","live":false,"age_seconds":9000}
				]`))
			})
			defer closeServer()

			ids, known := store.LiveSessionIDs(context.Background())

			Expect(known).To(BeTrue())
			Expect(ids).To(ConsistOf(liveID))
		})

		It("returns an empty, known set for an answered store with no live sessions", func() {
			store, closeServer := newStoreAt(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`[]`))
			})
			defer closeServer()

			ids, known := store.LiveSessionIDs(context.Background())

			Expect(known).To(BeTrue(), "an answered store is not 'cannot tell'")
			Expect(ids).To(BeEmpty())
		})

		It("returns cannot-tell for a non-200 response", func() {
			store, closeServer := newStoreAt(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			})
			defer closeServer()

			ids, known := store.LiveSessionIDs(context.Background())

			Expect(known).To(BeFalse())
			Expect(ids).To(BeNil())
		})

		It("returns cannot-tell for an unparseable body", func() {
			store, closeServer := newStoreAt(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`not json`))
			})
			defer closeServer()

			_, known := store.LiveSessionIDs(context.Background())

			Expect(known).To(BeFalse())
		})

		It("returns cannot-tell for an unreachable store", func() {
			server := httptest.NewServer(http.NotFoundHandler())
			store := heartbeat.NewStore(server.URL, nil)
			server.Close()

			_, known := store.LiveSessionIDs(context.Background())

			Expect(known).To(BeFalse())
		})
	})

	Describe("IsLive", func() {
		It("reports a live row", func() {
			store, closeServer := newStoreAt(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal("/api/1.0/session-heartbeat/" + liveID))
				_, _ = w.Write([]byte(`{"session_id":"` + liveID + `","live":true}`))
			})
			defer closeServer()

			live, known := store.IsLive(context.Background(), liveID)

			Expect(known).To(BeTrue())
			Expect(live).To(BeTrue())
		})

		It("treats a 404 as never-posted: not live, but known", func() {
			store, closeServer := newStoreAt(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			})
			defer closeServer()

			live, known := store.IsLive(context.Background(), "never-posted")

			Expect(known).To(BeTrue(), "a 404 is an answer, not a transport failure")
			Expect(live).To(BeFalse())
		})

		It("returns cannot-tell for an unreachable store", func() {
			server := httptest.NewServer(http.NotFoundHandler())
			store := heartbeat.NewStore(server.URL, nil)
			server.Close()

			live, known := store.IsLive(context.Background(), liveID)

			Expect(known).To(BeFalse())
			Expect(live).To(BeFalse())
		})

		It("treats a blank session id as not live without a request", func() {
			store, closeServer := newStoreAt(func(w http.ResponseWriter, _ *http.Request) {
				Fail("no request should be made for a blank session id")
			})
			defer closeServer()

			live, known := store.IsLive(context.Background(), "")

			Expect(known).To(BeTrue())
			Expect(live).To(BeFalse())
		})
	})

	It("defaults a blank base URL to the store's local address", func() {
		Expect(heartbeat.DefaultBaseURL).To(Equal("http://localhost:18080"))
		Expect(heartbeat.NewStore("", nil)).NotTo(BeNil())
	})
})
