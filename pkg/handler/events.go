// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/bborbe/vault-ui/pkg/events"
)

// DefaultEventsHeartbeat is how often an idle stream sends a comment line. It
// keeps a proxy from closing a connection that has nothing to say, and it is
// short enough that a dead peer is noticed rather than lingering.
const DefaultEventsHeartbeat = 15 * time.Second

// NewEventsHandler serves the board's live updates as Server-Sent Events.
//
// SSE rather than WebSocket: the page never streams data back, SSE rides plain
// HTTP and reconnects on its own. The stream carries no payload — a `board`
// event means "read the board again" — so the page keeps one code path that
// builds the model from the JSON endpoints.
func NewEventsHandler(hub *events.Hub, heartbeat time.Duration) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		flusher, ok := resp.(http.Flusher)
		if !ok {
			http.Error(resp, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		resp.Header().Set("Content-Type", "text/event-stream")
		resp.Header().Set("Cache-Control", "no-cache")
		resp.Header().Set("Connection", "keep-alive")
		resp.WriteHeader(http.StatusOK)

		// The first event tells the client the stream is live, so it does not
		// wait for a change before trusting the connection.
		if _, err := fmt.Fprint(resp, "event: board\ndata: {}\n\n"); err != nil {
			return
		}
		flusher.Flush()

		notifications, unsubscribe := hub.Subscribe()
		defer unsubscribe()

		ticker := time.NewTicker(heartbeat)
		defer ticker.Stop()

		for {
			select {
			case <-req.Context().Done():
				return
			case _, open := <-notifications:
				if !open {
					return
				}
				if _, err := fmt.Fprint(resp, "event: board\ndata: {}\n\n"); err != nil {
					return
				}
				flusher.Flush()
			case <-ticker.C:
				// A comment line is ignored by EventSource and keeps the
				// connection alive through an idle proxy.
				if _, err := fmt.Fprint(resp, ": ping\n\n"); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})
}
