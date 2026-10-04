// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package websocket_test

import (
	"context"
	stderrors "errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/websocket"
	"github.com/bborbe/vault-ui/pkg/websocket/mocks"
)

// fakeConn is an in-memory websocket.Conn stand-in. It records every written
// frame and signals each write on a channel so tests can wait deterministically.
type fakeConn struct {
	mu          sync.Mutex
	written     [][]byte
	closed      bool
	writeErr    error
	deadlineErr error
	signal      chan []byte
}

func newFakeConn() *fakeConn {
	return &fakeConn{signal: make(chan []byte, 1024)}
}

func (f *fakeConn) WriteMessage(_ int, data []byte) error {
	f.mu.Lock()
	if f.writeErr != nil {
		err := f.writeErr
		f.mu.Unlock()
		return err
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	f.written = append(f.written, cp)
	f.mu.Unlock()
	f.signal <- cp
	return nil
}

func (f *fakeConn) SetWriteDeadline(time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deadlineErr
}

func (f *fakeConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeConn) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeConn) frames() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.written))
	copy(out, f.written)
	return out
}

var _ = Describe("ConnectionManager", func() {
	var metrics *mocks.WebsocketMetrics
	var manager websocket.ConnectionManager

	BeforeEach(func() {
		metrics = &mocks.WebsocketMetrics{}
		manager = websocket.NewConnectionManager(metrics)
	})

	Describe("Broadcast", func() {
		It("reaches every connected client", func() {
			first, err := manager.Connect(newFakeConn())
			Expect(err).NotTo(HaveOccurred())
			second, err := manager.Connect(newFakeConn())
			Expect(err).NotTo(HaveOccurred())

			manager.Broadcast([]byte(`{"type":"task_updated"}`))

			Expect(<-first.Messages()).To(Equal([]byte(`{"type":"task_updated"}`)))
			Expect(<-second.Messages()).To(Equal([]byte(`{"type":"task_updated"}`)))
			Expect(manager.Count()).To(Equal(2))
			Expect(metrics.BroadcastCallCount()).To(Equal(1))
		})

		It("drops a client whose queue is full instead of blocking", func() {
			conn := newFakeConn()
			client, err := manager.Connect(conn)
			Expect(err).NotTo(HaveOccurred())

			// Fill the queue without pumping it, then one more broadcast must
			// drop the client rather than block.
			for i := 0; i < websocket.DefaultBufferSize; i++ {
				manager.Broadcast([]byte("frame"))
			}
			Expect(manager.Count()).To(Equal(1))

			done := make(chan struct{})
			go func() {
				manager.Broadcast([]byte("overflow"))
				close(done)
			}()
			Eventually(done, time.Second).Should(BeClosed())

			Expect(manager.Count()).To(Equal(0))
			Expect(conn.isClosed()).To(BeTrue())
			Expect(metrics.ClientDroppedCallCount()).To(Equal(1))
			Expect(metrics.ClientDisconnectedCallCount()).To(Equal(1))
			_ = client
		})
	})

	Describe("Send", func() {
		It("queues a frame for one client only", func() {
			first, err := manager.Connect(newFakeConn())
			Expect(err).NotTo(HaveOccurred())
			second, err := manager.Connect(newFakeConn())
			Expect(err).NotTo(HaveOccurred())

			manager.Send(first, []byte("pong"))

			Expect(<-first.Messages()).To(Equal([]byte("pong")))
			Expect(second.Messages()).To(BeEmpty())
		})

		It("is a no-op for an unregistered client", func() {
			client, err := manager.Connect(newFakeConn())
			Expect(err).NotTo(HaveOccurred())
			manager.Disconnect(client)
			Expect(func() { manager.Send(client, []byte("pong")) }).NotTo(Panic())
		})

		It("drops the client when its queue is full", func() {
			conn := newFakeConn()
			client, err := manager.Connect(conn)
			Expect(err).NotTo(HaveOccurred())
			for i := 0; i < websocket.DefaultBufferSize; i++ {
				manager.Send(client, []byte("frame"))
			}
			manager.Send(client, []byte("overflow"))
			Expect(manager.Count()).To(Equal(0))
			Expect(conn.isClosed()).To(BeTrue())
		})
	})

	Describe("Connect", func() {
		It("caps the number of concurrent clients", func() {
			for i := 0; i < websocket.DefaultMaxClients; i++ {
				_, err := manager.Connect(newFakeConn())
				Expect(err).NotTo(HaveOccurred())
			}
			_, err := manager.Connect(newFakeConn())
			Expect(err).To(MatchError(websocket.ErrTooManyClients))
			Expect(manager.Count()).To(Equal(websocket.DefaultMaxClients))
		})
	})

	Describe("Disconnect", func() {
		It("deregisters and closes the client", func() {
			conn := newFakeConn()
			client, err := manager.Connect(conn)
			Expect(err).NotTo(HaveOccurred())

			manager.Disconnect(client)

			Expect(manager.Count()).To(Equal(0))
			Expect(conn.isClosed()).To(BeTrue())
			Expect(metrics.ClientDisconnectedCallCount()).To(Equal(1))
		})

		It("is safe to call twice", func() {
			client, err := manager.Connect(newFakeConn())
			Expect(err).NotTo(HaveOccurred())
			manager.Disconnect(client)
			Expect(func() { manager.Disconnect(client) }).NotTo(Panic())
		})
	})

	Describe("Pump", func() {
		It("writes queued frames to the socket", func() {
			conn := newFakeConn()
			client, err := manager.Connect(conn)
			Expect(err).NotTo(HaveOccurred())

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pumpErr := make(chan error, 1)
			go func() { pumpErr <- manager.Pump(ctx, client) }()

			manager.Broadcast([]byte("hello"))
			Eventually(conn.signal, time.Second).Should(Receive(Equal([]byte("hello"))))

			cancel()
			Eventually(pumpErr, time.Second).Should(Receive(BeNil()))
			Expect(conn.frames()).To(HaveLen(1))
		})

		It("returns when the queue is closed", func() {
			client, err := manager.Connect(newFakeConn())
			Expect(err).NotTo(HaveOccurred())
			pumpErr := make(chan error, 1)
			go func() { pumpErr <- manager.Pump(context.Background(), client) }()
			manager.Disconnect(client)
			Eventually(pumpErr, time.Second).Should(Receive(BeNil()))
		})

		It("fails when the write deadline cannot be set", func() {
			conn := newFakeConn()
			conn.deadlineErr = stderrors.New("deadline")
			client, err := manager.Connect(conn)
			Expect(err).NotTo(HaveOccurred())
			pumpErr := make(chan error, 1)
			go func() { pumpErr <- manager.Pump(context.Background(), client) }()
			manager.Broadcast([]byte("frame"))
			Eventually(pumpErr, time.Second).Should(Receive(MatchError(ContainSubstring("deadline"))))
		})

		It("fails when the socket write fails", func() {
			conn := newFakeConn()
			conn.writeErr = stderrors.New("write failed")
			client, err := manager.Connect(conn)
			Expect(err).NotTo(HaveOccurred())
			pumpErr := make(chan error, 1)
			go func() { pumpErr <- manager.Pump(context.Background(), client) }()
			manager.Broadcast([]byte("frame"))
			Eventually(pumpErr, time.Second).Should(Receive(MatchError(ContainSubstring("write"))))
		})
	})
})
