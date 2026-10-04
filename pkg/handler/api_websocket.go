// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/bborbe/run"
	gws "github.com/gorilla/websocket"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/websocket"
)

const (
	// wsCloseServerNotReady is the close code the Python backend sends when its
	// connection manager is not ready.
	wsCloseServerNotReady = 1011
	// wsCloseTooManyClients is the close code for a refused connection at the
	// concurrent-client cap.
	wsCloseTooManyClients = 1013
	// wsCloseWait bounds the close-frame write.
	wsCloseWait = time.Second
)

// NewWebSocketHandler returns the /ws handler. It accepts every origin (the
// Python backend performs no origin check and the service is local-only),
// answers a client "ping" with "pong", ignores every other client message, and
// streams board events pushed by the connection manager. No frame is sent on
// connect.
func NewWebSocketHandler(
	readiness vaultui.Readiness,
	manager websocket.ConnectionManager,
) http.Handler {
	upgrader := gws.Upgrader{
		// The Python backend performs no origin check on /ws; rejecting an
		// origin would be a parity divergence.
		CheckOrigin: func(*http.Request) bool { return true },
	}
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		conn, err := upgrader.Upgrade(resp, req, nil)
		if err != nil {
			// Upgrade already wrote an HTTP error response.
			return
		}
		if !readiness.IsReady() {
			closeWith(conn, wsCloseServerNotReady, "Server not ready")
			return
		}
		client, err := manager.Connect(conn)
		if err != nil {
			closeWith(conn, wsCloseTooManyClients, "too many clients")
			return
		}
		defer manager.Disconnect(client)

		// The read loop and the write pump run until either finishes (client
		// disconnect, write failure, or server shutdown). closeOnCancel closes
		// the socket when the request context is cancelled, which unblocks the
		// read loop so shutdown never hangs.
		_ = run.CancelOnFirstFinishWait(req.Context(),
			func(ctx context.Context) error { return readLoop(ctx, conn, manager, client) },
			func(ctx context.Context) error { return manager.Pump(ctx, client) },
			func(ctx context.Context) error { return closeOnCancel(ctx, conn) },
		)
	})
}

// closeOnCancel closes the connection once ctx is cancelled.
func closeOnCancel(ctx context.Context, conn *gws.Conn) error {
	<-ctx.Done()
	_ = conn.Close()
	return nil
}

// readLoop consumes client messages until the connection closes. A text "ping"
// is answered with "pong" (queued through the client's send channel so it is
// written by the single writer); every other message is ignored.
func readLoop(
	ctx context.Context,
	conn *gws.Conn,
	manager websocket.ConnectionManager,
	client *websocket.Client,
) error {
	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			// Normal disconnect or a closed socket; not an error.
			return nil
		}
		if messageType != gws.TextMessage {
			continue
		}
		if string(payload) == "ping" {
			manager.Send(client, []byte("pong"))
		}
	}
}

// closeWith sends a WebSocket close frame with the given code and reason, then
// closes the socket.
func closeWith(conn *gws.Conn, code int, reason string) {
	_ = conn.WriteControl(
		gws.CloseMessage,
		gws.FormatCloseMessage(code, reason),
		time.Now().Add(wsCloseWait),
	)
	_ = conn.Close()
}
