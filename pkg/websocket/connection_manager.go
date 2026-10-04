// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package websocket implements the live-update channel of the vault-ui HTTP
// surface: a bounded, non-blocking connection manager and the JSON frames the
// Python backend broadcasts for vault changes and board mutations. It mirrors
// src/vault_ui/websocket/connection_manager.py, with two safety properties the
// Python backend lacks — a concurrent-client cap and a per-client buffered send
// queue so a slow client can never stall the watcher broadcast.
package websocket

import (
	"context"
	stderrors "errors"
	"sync"
	"time"

	"github.com/bborbe/errors"
	gws "github.com/gorilla/websocket"
)

const (
	// DefaultMaxClients caps concurrent connections. It is far above the
	// handful of clients the board opens, so it cannot affect parity.
	DefaultMaxClients = 64
	// DefaultBufferSize is the per-client outbound queue depth. A client that
	// falls further behind than this is dropped rather than blocking.
	DefaultBufferSize = 256
	// writeWait bounds a single socket write.
	writeWait = 5 * time.Second
)

// ErrTooManyClients is returned by Connect when the concurrent-client cap is
// reached.
var ErrTooManyClients = stderrors.New("too many websocket clients")

// Conn is the subset of *websocket.Conn the connection manager writes to. It is
// an interface so the manager can be exercised without a live socket.
type Conn interface {
	WriteMessage(messageType int, data []byte) error
	SetWriteDeadline(t time.Time) error
	Close() error
}

// Client is a registered connection with its own buffered outbound queue.
type Client struct {
	conn Conn
	send chan []byte
}

// Messages returns the client's outbound queue. The write pump drains it;
// tests read it to observe broadcasts without a socket.
func (c *Client) Messages() <-chan []byte { return c.send }

// ConnectionManager tracks connected clients and fans board events out to them.
type ConnectionManager interface {
	// Connect registers an upgraded connection, returning ErrTooManyClients
	// when the cap is reached.
	Connect(conn Conn) (*Client, error)
	// Disconnect deregisters a client and closes it. It is safe to call twice.
	Disconnect(c *Client)
	// Send enqueues a frame for one client, dropping it when its queue is full.
	Send(c *Client, payload []byte)
	// Broadcast enqueues a frame for every client. A client whose queue is full
	// is dropped rather than allowed to block the caller.
	Broadcast(payload []byte)
	// Pump writes the client's queued frames to its socket until the queue is
	// closed or ctx is cancelled.
	Pump(ctx context.Context, c *Client) error
	// Count returns the number of registered clients.
	Count() int
}

type manager struct {
	mu      sync.Mutex
	clients map[*Client]struct{}
	max     int
	buffer  int
	metrics Metrics
}

// NewConnectionManager returns a bounded, non-blocking connection manager.
func NewConnectionManager(metrics Metrics) ConnectionManager {
	return &manager{
		clients: make(map[*Client]struct{}),
		max:     DefaultMaxClients,
		buffer:  DefaultBufferSize,
		metrics: metrics,
	}
}

// Connect registers an upgraded connection.
func (m *manager) Connect(conn Conn) (*Client, error) {
	m.mu.Lock()
	if len(m.clients) >= m.max {
		m.mu.Unlock()
		return nil, ErrTooManyClients
	}
	c := &Client{conn: conn, send: make(chan []byte, m.buffer)}
	m.clients[c] = struct{}{}
	m.mu.Unlock()
	m.metrics.ClientConnected()
	return c, nil
}

// Disconnect removes a client, closes its queue, and closes its socket. The
// queue is closed under the manager lock so a concurrent Broadcast can never
// send on a closed channel.
func (m *manager) Disconnect(c *Client) {
	m.mu.Lock()
	_, registered := m.clients[c]
	if registered {
		delete(m.clients, c)
		close(c.send)
	}
	m.mu.Unlock()
	if !registered {
		return
	}
	m.metrics.ClientDisconnected()
	_ = c.conn.Close()
}

// Send enqueues a frame for a single client.
func (m *manager) Send(c *Client, payload []byte) {
	m.mu.Lock()
	if _, registered := m.clients[c]; !registered {
		m.mu.Unlock()
		return
	}
	select {
	case c.send <- payload:
		m.mu.Unlock()
	default:
		delete(m.clients, c)
		close(c.send)
		m.mu.Unlock()
		m.metrics.ClientDropped()
		m.metrics.ClientDisconnected()
		_ = c.conn.Close()
	}
}

// Broadcast enqueues a frame for every registered client. The lock is held only
// for the non-blocking channel sends, never while writing to a socket.
func (m *manager) Broadcast(payload []byte) {
	m.mu.Lock()
	var dropped []*Client
	for c := range m.clients {
		select {
		case c.send <- payload:
		default:
			delete(m.clients, c)
			close(c.send)
			dropped = append(dropped, c)
		}
	}
	m.mu.Unlock()

	m.metrics.Broadcast()
	for _, c := range dropped {
		m.metrics.ClientDropped()
		m.metrics.ClientDisconnected()
		_ = c.conn.Close()
	}
}

// Pump writes queued frames to the client socket until the queue is closed or
// ctx is cancelled. It is the only writer for its connection.
func (m *manager) Pump(ctx context.Context, c *Client) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case payload, ok := <-c.send:
			if !ok {
				return nil
			}
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				return errors.Wrap(ctx, err, "set write deadline")
			}
			if err := c.conn.WriteMessage(gws.TextMessage, payload); err != nil {
				return errors.Wrap(ctx, err, "write websocket message")
			}
		}
	}
}

// Count returns the number of registered clients.
func (m *manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.clients)
}
