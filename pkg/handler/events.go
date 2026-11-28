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
// HTTP and reconnects on its own. A `board` event carries no payload — it means
// "read the board again" — so the page keeps one code path that builds the
// model from the JSON endpoints. The hub's typed events (`write_failed`) are
// written with their JSON payload, because a refused write changes nothing a
// re-read could reveal.
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
		// The server's WriteTimeout is an absolute per-request deadline, so a
		// long-lived stream would be truncated mid-response and the browser
		// would log ERR_INCOMPLETE_CHUNKED_ENCODING. Clear the deadline for
		// this response only; the heartbeat still detects a dead peer. An
		// unsupported writer (the test recorder) simply keeps its own deadline.
		_ = http.NewResponseController(resp).SetWriteDeadline(time.Time{})
		resp.WriteHeader(http.StatusOK)

		// The first event tells the client the stream is live, so it does not
		// wait for a change before trusting the connection.
		if _, err := fmt.Fprint(resp, "event: board\ndata: {}\n\n"); err != nil {
			return
		}
		flusher.Flush()

		notifications, unsubscribe := hub.Subscribe()
		defer unsubscribe()
		typed, unsubscribeEvents := hub.SubscribeEvents()
		defer unsubscribeEvents()

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
			case event, open := <-typed:
				if !open {
					return
				}
				if _, err := fmt.Fprintf(resp, "event: %s\ndata: %s\n\n", event.Name, event.Data); err != nil {
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
