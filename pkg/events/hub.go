// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package events fans board-change notifications out to Server-Sent Events
// subscribers.
//
// It carries no payload: a notification means "the board changed, read it
// again". That keeps the stream a wake-up signal rather than a second copy of
// the data, so a subscriber that misses one still converges on the next read.
package events

import "sync"

// Hub is a non-blocking fan-out. A subscriber that cannot keep up loses
// notifications rather than blocking the publisher — the next notification it
// does receive makes it refetch the current state anyway.
type Hub struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{subs: map[chan struct{}]struct{}{}}
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
