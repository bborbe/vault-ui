// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package openable_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/openable"
)

var now = time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

func fixedClock() libtime.CurrentDateTimeGetter {
	return libtime.CurrentDateTimeGetterFunc(func() libtime.DateTime { return libtime.DateTime(now) })
}

// todaysPayload is the wrapper's current shape: no waiting_on.
const todaysPayload = `{"rows":[
	{"vault":"private-personal","kind":"task","name":"Attention Watchers Are Pushed","session_id":"s1","decision":"held","reason":"progress-stalled","phase":"execution","flag_set_by":"operator"},
	{"vault":"private-personal","kind":"task","name":"Open Me","session_id":"","decision":"open","reason":"","phase":"todo","flag_set_by":"operator"},
	{"vault":"private-personal","kind":"goal","name":"Some Goal","session_id":"s2","decision":"held","reason":"not-absent","phase":"execution","flag_set_by":"operator"}
]}`

// waitingPayload carries the waiting_on contract being added to the wrapper.
const waitingPayload = `{"rows":[
	{"vault":"private-personal","kind":"task","name":"Diagnosis Still Requires the Operator","decision":"held","reason":"parked-until",
	 "waiting_on":{"summary":"re-check after deploy","since":"2026-11-27T08:00:00Z","until":"2026-11-27T14:00:00Z","card_id":"c1","pr_url":"https://github.com/bborbe/x/pull/1","blockers":["A"],"session_id":"s9"}}
]}`

func serve(payload string, status int) (*httptest.Server, *int32) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		Expect(r.URL.Path).To(Equal("/api/1.0/openable"))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	return server, &hits
}

var _ = Describe("Client", func() {
	It("parses today's rows, tolerating an absent waiting_on", func() {
		server, _ := serve(todaysPayload, http.StatusOK)
		defer server.Close()

		rows, err := openable.NewClient(server.URL, nil).Rows(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(3))
		Expect(rows[0].Reason).To(Equal("progress-stalled"))
		Expect(rows[0].WaitingOn).To(BeNil())
	})

	It("parses waiting_on when present", func() {
		server, _ := serve(waitingPayload, http.StatusOK)
		defer server.Close()

		rows, err := openable.NewClient(server.URL, nil).Rows(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(rows[0].WaitingOn).NotTo(BeNil())
		Expect(rows[0].WaitingOn.Summary).To(Equal("re-check after deploy"))
		Expect(rows[0].WaitingOn.Blockers).To(Equal([]string{"A"}))
	})

	It("errors on a non-200", func() {
		server, _ := serve(`oops`, http.StatusInternalServerError)
		defer server.Close()

		_, err := openable.NewClient(server.URL, nil).Rows(context.Background())
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Label", func() {
	DescribeTable("maps each reason code",
		func(reason, until, want string) {
			Expect(openable.Label(reason, until, now, time.UTC)).To(Equal(want))
		},
		Entry("stalled", "progress-stalled", "", "⏸ stalled"),
		Entry("blocked", "blocked", "", "⛔ blocked"),
		Entry("live elsewhere", "not-absent", "", "● live elsewhere"),
		Entry("PR", "parked-on-pr", "", "🔗 waiting on PR"),
		Entry("card", "parked-on-card", "", "🃏 waiting on card"),
		Entry("parked, no until", "parked-until", "", "⏰ parked"),
		Entry("parked until today", "parked-until", "2026-11-27T14:00:00Z", "⏰ parked until 14:00"),
		Entry("parked until another day", "parked-until", "2026-11-28T09:30:00Z", "⏰ parked until Nov 28 09:30"),
		Entry("parked, unparseable until", "parked-until", "tomorrow", "⏰ parked"),
		Entry("unknown code", "something-new", "", "⏸ held: something-new"),
		Entry("empty code", "", "", "⏸ held"),
	)

	It("renders parked-until in the given location", func() {
		berlin := time.FixedZone("CET", 3600)
		Expect(openable.Label("parked-until", "2026-11-27T13:00:00Z", now, berlin)).
			To(Equal("⏰ parked until 14:00"))
	})
})

var _ = Describe("NewHold", func() {
	It("returns nil for an open row", func() {
		Expect(openable.NewHold(openable.Row{Decision: "open"}, now, time.UTC)).To(BeNil())
	})

	It("carries waiting_on detail and keeps absent fields null", func() {
		hold := openable.NewHold(openable.Row{
			Decision: "held",
			Reason:   "parked-on-pr",
			WaitingOn: &openable.WaitingOn{
				Summary: "PR review",
				PRURL:   "https://github.com/bborbe/x/pull/1",
			},
		}, now, time.UTC)
		Expect(hold).NotTo(BeNil())
		Expect(hold.Label).To(Equal("🔗 waiting on PR"))
		Expect(*hold.Summary).To(Equal("PR review"))
		Expect(*hold.PRURL).To(Equal("https://github.com/bborbe/x/pull/1"))
		Expect(hold.CardID).To(BeNil())
		Expect(hold.Until).To(BeNil())
		Expect(hold.Blockers).To(Equal([]string{}))
	})
})

var _ = Describe("Holds", func() {
	It("indexes held rows by vault, kind and name", func() {
		server, _ := serve(todaysPayload, http.StatusOK)
		defer server.Close()
		holds := openable.NewHolds(openable.HoldsParams{
			Client: openable.NewClient(server.URL, nil), Clock: fixedClock(),
		})

		Expect(holds.Refresh(context.Background())).To(BeTrue())
		hold := holds.Hold("private-personal", "task", "Attention Watchers Are Pushed")
		Expect(hold).NotTo(BeNil())
		Expect(hold.Label).To(Equal("⏸ stalled"))
		Expect(holds.Hold("private-personal", "task", "Open Me")).To(BeNil())
		Expect(holds.Hold("private-personal", "task", "Some Goal")).To(BeNil())
		Expect(holds.Hold("private-personal", "goal", "Some Goal")).NotTo(BeNil())
	})

	It("advances the generation only when the held set changes", func() {
		server, _ := serve(todaysPayload, http.StatusOK)
		defer server.Close()
		var changed int32
		holds := openable.NewHolds(openable.HoldsParams{
			Client:  openable.NewClient(server.URL, nil),
			Clock:   fixedClock(),
			Changed: func() { atomic.AddInt32(&changed, 1) },
		})

		holds.Refresh(context.Background())
		generation := holds.Generation()
		Expect(holds.Refresh(context.Background())).To(BeFalse())
		Expect(holds.Generation()).To(Equal(generation))
		Expect(atomic.LoadInt32(&changed)).To(Equal(int32(1)))
	})

	It("fails open: an unreachable wrapper empties the held set without error", func() {
		server, _ := serve(todaysPayload, http.StatusOK)
		client := openable.NewClient(server.URL, nil)
		holds := openable.NewHolds(openable.HoldsParams{Client: client, Clock: fixedClock()})
		holds.Refresh(context.Background())
		Expect(holds.Hold("private-personal", "goal", "Some Goal")).NotTo(BeNil())

		server.Close()

		Expect(holds.Refresh(context.Background())).To(BeTrue())
		Expect(holds.Hold("private-personal", "goal", "Some Goal")).To(BeNil())
	})

	It("holds nothing before the first refresh", func() {
		holds := openable.NewHolds(openable.HoldsParams{
			Client: openable.NewClient("http://127.0.0.1:1", nil), Clock: fixedClock(),
		})
		Expect(holds.Hold("private-personal", "goal", "Some Goal")).To(BeNil())
		Expect(holds.Generation()).To(Equal(uint64(0)))
	})

	It("Run returns nil when the context is cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		holds := openable.NewHolds(openable.HoldsParams{
			Client:   openable.NewClient("http://127.0.0.1:1", nil),
			Clock:    fixedClock(),
			Interval: time.Millisecond,
		})
		done := make(chan error, 1)
		go func() { done <- holds.Run(ctx) }()
		time.Sleep(10 * time.Millisecond)
		cancel()
		Eventually(done).Should(Receive(BeNil()))
	})
})
