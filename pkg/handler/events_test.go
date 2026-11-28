// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bborbe/vault-ui/pkg/events"
	"github.com/bborbe/vault-ui/pkg/handler"
)

// streamRecorder is a concurrency-safe ResponseWriter: the handler writes from
// its own goroutine while the test reads, so the standard httptest recorder
// would race under -race.
type streamRecorder struct {
	mu      sync.Mutex
	body    bytes.Buffer
	header  http.Header
	status  int
	flushes int
}

func newStreamRecorder() *streamRecorder {
	return &streamRecorder{header: http.Header{}, status: http.StatusOK}
}

func (r *streamRecorder) Header() http.Header { return r.header }

func (r *streamRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.Write(p)
}

func (r *streamRecorder) WriteHeader(status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = status
}

func (r *streamRecorder) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushes++
}

func (r *streamRecorder) bodyString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

func (r *streamRecorder) boardEvents() int {
	return strings.Count(r.bodyString(), "event: board")
}

// waitForEventCount polls the recorder until it holds want board events.
func waitForEventCount(t *testing.T, recorder *streamRecorder, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if recorder.boardEvents() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("expected %d board events, got %d after 2s", want, recorder.boardEvents())
}

func TestEventsHandlerStreamsOnChangeOnly(t *testing.T) {
	hub := events.NewHub()
	eventsHandler := handler.NewEventsHandler(hub, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	recorder := newStreamRecorder()

	done := make(chan struct{})
	go func() {
		eventsHandler.ServeHTTP(recorder, req)
		close(done)
	}()

	// The first event tells the client the stream is live.
	waitForEventCount(t, recorder, 1)
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}

	// The negative control: with nothing changed, the stream must stay silent.
	// Without this the test would pass on a handler that emits unconditionally.
	time.Sleep(50 * time.Millisecond)
	if got := recorder.boardEvents(); got != 1 {
		t.Fatalf("expected no event without a change, got %d", got)
	}

	hub.Notify()
	waitForEventCount(t, recorder, 2)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after the request context was cancelled")
	}
}

func TestEventsHandlerSendsHeartbeatComments(t *testing.T) {
	hub := events.NewHub()
	eventsHandler := handler.NewEventsHandler(hub, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	recorder := newStreamRecorder()

	done := make(chan struct{})
	go func() {
		eventsHandler.ServeHTTP(recorder, req)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(recorder.bodyString(), ": ping") {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(recorder.bodyString(), ": ping") {
		t.Fatal("expected a heartbeat comment on an idle stream")
	}
	// A heartbeat is a comment, not an event: it must not wake the client.
	if got := recorder.boardEvents(); got != 1 {
		t.Fatalf("heartbeat must not emit a board event, got %d", got)
	}

	cancel()
	<-done
}

func TestEventsHandlerStreamsTypedEventsWithTheirPayload(t *testing.T) {
	hub := events.NewHub()
	eventsHandler := handler.NewEventsHandler(hub, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	recorder := newStreamRecorder()

	done := make(chan struct{})
	go func() {
		eventsHandler.ServeHTTP(recorder, req)
		close(done)
	}()
	waitForEventCount(t, recorder, 1)

	hub.Publish(events.Event{
		Name: events.WriteFailedEvent,
		Data: []byte(`{"type":"write_failed","task_id":"Task A"}`),
	})

	want := "event: write_failed\ndata: {\"type\":\"write_failed\",\"task_id\":\"Task A\"}\n\n"
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(recorder.bodyString(), want) {
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(recorder.bodyString(), want) {
		t.Fatalf("expected %q on the stream, got %q", want, recorder.bodyString())
	}
	// A typed event is not a board wake-up.
	if got := recorder.boardEvents(); got != 1 {
		t.Fatalf("a typed event must not emit a board event, got %d", got)
	}

	cancel()
	<-done
}
