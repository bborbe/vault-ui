// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package events_test

import (
	"testing"
	"time"

	"github.com/bborbe/vault-ui/pkg/events"
)

// waitFor polls until condition holds or the deadline passes. It is the only
// timing primitive the hub's tests use, so a slow machine costs latency rather
// than a flake.
func waitFor(t *testing.T, condition func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

func TestNotifyWakesSubscriber(t *testing.T) {
	hub := events.NewHub()
	notifications, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	hub.Notify()

	select {
	case <-notifications:
	case <-time.After(2 * time.Second):
		t.Fatal("Notify did not wake the subscriber")
	}
}

func TestNotifySendsNothingWithoutASubscriber(t *testing.T) {
	hub := events.NewHub()
	// The negative control for the test above: with no subscriber the hub must
	// not panic and must not block. If this hung, the non-blocking send is what
	// broke.
	done := make(chan struct{})
	go func() {
		hub.Notify()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Notify blocked with no subscriber")
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	hub := events.NewHub()
	notifications, unsubscribe := hub.Subscribe()
	unsubscribe()
	// A second call must be safe — the handler defers it, and a disconnect path
	// can reach it twice.
	unsubscribe()

	hub.Notify()

	select {
	case _, open := <-notifications:
		if open {
			t.Fatal("a notification arrived after unsubscribe")
		}
	case <-time.After(100 * time.Millisecond):
		// The channel is closed by unsubscribe, so a receive returns
		// immediately; reaching here means it was already drained, which is
		// equally acceptable.
	}
}

func TestNotifyCoalescesWhenTheSubscriberIsBusy(t *testing.T) {
	hub := events.NewHub()
	notifications, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	// Two notifies with nobody reading must leave exactly one pending wake-up,
	// not block and not queue two.
	hub.Notify()
	hub.Notify()

	if !waitFor(t, func() bool { return len(notifications) == 1 }) {
		t.Fatalf("expected exactly one pending notification, got %d", len(notifications))
	}
}

func TestPublishDeliversTheTypedEvent(t *testing.T) {
	hub := events.NewHub()
	typed, unsubscribe := hub.SubscribeEvents()
	defer unsubscribe()

	hub.Publish(events.Event{Name: events.WriteFailedEvent, Data: []byte(`{"task_id":"A"}`)})

	select {
	case event := <-typed:
		if event.Name != events.WriteFailedEvent || string(event.Data) != `{"task_id":"A"}` {
			t.Fatalf("unexpected event %q %q", event.Name, event.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Publish did not reach the event subscriber")
	}
}

func TestPublishDoesNotCoalesceDistinctEvents(t *testing.T) {
	hub := events.NewHub()
	typed, unsubscribe := hub.SubscribeEvents()
	defer unsubscribe()

	// Two refused writes are two pieces of news; unlike Notify, both must be
	// held for the subscriber.
	hub.Publish(events.Event{Name: events.WriteFailedEvent, Data: []byte(`{"task_id":"A"}`)})
	hub.Publish(events.Event{Name: events.WriteFailedEvent, Data: []byte(`{"task_id":"B"}`)})

	if !waitFor(t, func() bool { return len(typed) == 2 }) {
		t.Fatalf("expected two pending events, got %d", len(typed))
	}
}

func TestPublishNeverBlocksOnAFullSubscriber(t *testing.T) {
	hub := events.NewHub()
	_, unsubscribe := hub.SubscribeEvents()
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			hub.Publish(events.Event{Name: events.WriteFailedEvent, Data: []byte(`{}`)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a subscriber that never reads")
	}
}

func TestPublishDoesNotWakeBoardSubscribers(t *testing.T) {
	hub := events.NewHub()
	notifications, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	hub.Publish(events.Event{Name: events.WriteFailedEvent, Data: []byte(`{}`)})

	select {
	case <-notifications:
		t.Fatal("a typed event must not emit a board wake-up")
	case <-time.After(50 * time.Millisecond):
	}
}
