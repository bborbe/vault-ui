// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package events fans board-change notifications out to Server-Sent Events
// subscribers.
//
// A board notification carries no payload: it means "the board changed, read
// it again". That keeps the stream a wake-up signal rather than a second copy
// of the data, so a subscriber that misses one still converges on the next
// read.
//
// The one exception is a typed Event, for news a re-read cannot deliver: a
// queued write the server refused leaves the file unchanged, so only an explicit
// event tells the board to drop its optimistic value.
package events

import "sync"

// WriteFailedEvent is the SSE event name for a queued write the vault refused.
const WriteFailedEvent = "write_failed"

// eventBuffer is how many typed events a subscriber may hold unread. Typed
// events are rare (one per refused write), so a subscriber this far behind is
// stuck rather than slow.
const eventBuffer = 16

// Event is one typed notification: Name is the SSE event name, Data its
// single-line JSON payload.
type Event struct {
	Name string
	Data []byte
}

// Hub is a non-blocking fan-out. A subscriber that cannot keep up loses
// notifications rather than blocking the publisher — the next notification it
// does receive makes it refetch the current state anyway.
type Hub struct {
	mu        sync.Mutex
	subs      map[chan struct{}]struct{}
	eventSubs map[chan Event]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{
		subs:      map[chan struct{}]struct{}{},
		eventSubs: map[chan Event]struct{}{},
	}
}

// Publish sends a typed event to every event subscriber. Unlike Notify it does
// not coalesce — each event is distinct — but it still never blocks: a
// subscriber whose buffer is full loses the event, and the board's pending
// overlay then lapses on its own timeout instead.
func (h *Hub) Publish(event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for subscriber := range h.eventSubs {
		select {
		case subscriber <- event:
		default:
		}
	}
}

// SubscribeEvents registers a typed-event subscriber and returns its channel
// plus the function that deregisters it. The function is safe to call twice.
func (h *Hub) SubscribeEvents() (<-chan Event, func()) {
	typed := make(chan Event, eventBuffer)
	h.mu.Lock()
	h.eventSubs[typed] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.eventSubs, typed)
			// Closed under the lock: Publish sends under the same lock.
			close(typed)
			h.mu.Unlock()
		})
	}
	return typed, unsubscribe
}

// Notify wakes every subscriber. It never blocks: a subscriber that already has
// a pending wake-up is skipped, because one wake-up is as good as two.
func (h *Hub) Notify() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for subscriber := range h.subs {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
}

// Subscribe registers a subscriber and returns its channel plus the function
// that deregisters it. The function is safe to call twice.
func (h *Hub) Subscribe() (<-chan struct{}, func()) {
	notifications := make(chan struct{}, 1)
	h.mu.Lock()
	h.subs[notifications] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, notifications)
			// Closed under the lock: Notify sends under the same lock, so no
			// send can race the close.
			close(notifications)
			h.mu.Unlock()
		})
	}
	return notifications, unsubscribe
}
