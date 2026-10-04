// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler_test

import (
	stderrors "errors"
	"net/http/httptest"
	"strings"
	"time"

	gws "github.com/gorilla/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/handler"
	"github.com/bborbe/vault-ui/pkg/websocket"
	"github.com/bborbe/vault-ui/pkg/websocket/mocks"
)

// newWSServer starts a test server for the /ws handler and returns it with the
// manager backing it.
func newWSServer(readiness vaultui.Readiness) (*httptest.Server, websocket.ConnectionManager) {
	manager := websocket.NewConnectionManager(&mocks.WebsocketMetrics{})
	return httptest.NewServer(handler.NewWebSocketHandler(readiness, manager)), manager
}

func wsURL(server *httptest.Server) string {
	return "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
}

func readyGate() vaultui.Readiness {
	readiness := vaultui.NewReadiness()
	readiness.SetReady()
	return readiness
}

// stubConn satisfies websocket.Conn for filling the manager to its cap.
type stubConn struct{}

func (stubConn) WriteMessage(int, []byte) error   { return nil }
func (stubConn) SetWriteDeadline(time.Time) error { return nil }
func (stubConn) Close() error                     { return nil }

var _ = Describe("WebSocket handler", func() {
	It("closes with 1011 Server not ready before the service is ready", func() {
		server, _ := newWSServer(vaultui.NewReadiness())
		defer server.Close()

		conn, _, err := gws.DefaultDialer.Dial(wsURL(server), nil)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = conn.Close() }()

		_, _, err = conn.ReadMessage()
		var closeErr *gws.CloseError
		Expect(stderrors.As(err, &closeErr)).To(BeTrue())
		Expect(closeErr.Code).To(Equal(1011))
		Expect(closeErr.Text).To(Equal("Server not ready"))
	})

	It("sends no initial frame and answers ping with pong", func() {
		server, _ := newWSServer(readyGate())
		defer server.Close()

		conn, _, err := gws.DefaultDialer.Dial(wsURL(server), nil)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = conn.Close() }()

		Expect(conn.WriteMessage(gws.TextMessage, []byte("ping"))).To(Succeed())
		_, message, err := conn.ReadMessage()
		Expect(err).NotTo(HaveOccurred())
		// The pong is the very first frame: no frame is sent on connect.
		Expect(string(message)).To(Equal("pong"))
	})

	It("ignores other text messages without echoing or closing", func() {
		server, _ := newWSServer(readyGate())
		defer server.Close()

		conn, _, err := gws.DefaultDialer.Dial(wsURL(server), nil)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = conn.Close() }()

		Expect(conn.WriteMessage(gws.TextMessage, []byte("hello"))).To(Succeed())
		Expect(conn.WriteMessage(gws.TextMessage, []byte("ping"))).To(Succeed())
		_, message, err := conn.ReadMessage()
		Expect(err).NotTo(HaveOccurred())
		// The first frame is the pong — "hello" was neither echoed nor answered.
		Expect(string(message)).To(Equal("pong"))
	})

	It("does not treat a binary frame as a ping", func() {
		server, _ := newWSServer(readyGate())
		defer server.Close()

		conn, _, err := gws.DefaultDialer.Dial(wsURL(server), nil)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = conn.Close() }()

		Expect(conn.WriteMessage(gws.BinaryMessage, []byte("ping"))).To(Succeed())
		Expect(conn.WriteMessage(gws.TextMessage, []byte("ping"))).To(Succeed())
		_, message, err := conn.ReadMessage()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(message)).To(Equal("pong"))
	})

	It("deregisters the client on disconnect", func() {
		server, manager := newWSServer(readyGate())
		defer server.Close()

		conn, _, err := gws.DefaultDialer.Dial(wsURL(server), nil)
		Expect(err).NotTo(HaveOccurred())
		Eventually(manager.Count).Should(Equal(1))

		_ = conn.Close()
		Eventually(manager.Count).Should(Equal(0))
	})

	It("delivers a broadcast to a connected client", func() {
		server, manager := newWSServer(readyGate())
		defer server.Close()

		conn, _, err := gws.DefaultDialer.Dial(wsURL(server), nil)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = conn.Close() }()
		Eventually(manager.Count).Should(Equal(1))

		manager.Broadcast([]byte(`{"type":"task_updated","task_id":"TaskOne"}`))
		_, message, err := conn.ReadMessage()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(message)).To(Equal(`{"type":"task_updated","task_id":"TaskOne"}`))
	})

	It("refuses a connection once the client cap is reached", func() {
		manager := websocket.NewConnectionManager(&mocks.WebsocketMetrics{})
		for i := 0; i < websocket.DefaultMaxClients; i++ {
			_, err := manager.Connect(stubConn{})
			Expect(err).NotTo(HaveOccurred())
		}
		server := httptest.NewServer(handler.NewWebSocketHandler(readyGate(), manager))
		defer server.Close()

		conn, _, err := gws.DefaultDialer.Dial(wsURL(server), nil)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = conn.Close() }()

		_, _, err = conn.ReadMessage()
		var closeErr *gws.CloseError
		Expect(stderrors.As(err, &closeErr)).To(BeTrue())
		Expect(closeErr.Code).To(Equal(1013))
	})
})
