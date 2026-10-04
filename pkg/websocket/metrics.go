// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package websocket

import "github.com/prometheus/client_golang/prometheus"

//counterfeiter:generate -o ./mocks/websocket-metrics.go --fake-name WebsocketMetrics . Metrics

// Metrics records live-channel activity. It is injected so the connection
// manager stays independent of the Prometheus registry.
type Metrics interface {
	ClientConnected()
	ClientDisconnected()
	ClientDropped()
	Broadcast()
}

var (
	connectedClientsGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "vault_ui",
		Subsystem: "websocket",
		Name:      "connected_clients",
		Help:      "Number of currently connected WebSocket clients.",
	})
	clientsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "vault_ui",
		Subsystem: "websocket",
		Name:      "clients_connected_total",
		Help:      "Total WebSocket clients connected.",
	})
	clientsDisconnectedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "vault_ui",
		Subsystem: "websocket",
		Name:      "clients_disconnected_total",
		Help:      "Total WebSocket clients disconnected.",
	})
	clientsDroppedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "vault_ui",
		Subsystem: "websocket",
		Name:      "clients_dropped_total",
		Help:      "Total WebSocket clients dropped for a full send queue.",
	})
	broadcastTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "vault_ui",
		Subsystem: "websocket",
		Name:      "broadcast_total",
		Help:      "Total broadcast frames enqueued.",
	})
)

func init() {
	prometheus.MustRegister(
		connectedClientsGauge,
		clientsTotal,
		clientsDisconnectedTotal,
		clientsDroppedTotal,
		broadcastTotal,
	)
	// Pre-initialize the counters so alerting rules see the series from boot.
	clientsTotal.Add(0)
	clientsDisconnectedTotal.Add(0)
	clientsDroppedTotal.Add(0)
	broadcastTotal.Add(0)
}

type prometheusMetrics struct{}

// NewMetrics returns the Prometheus-backed live-channel metrics.
func NewMetrics() Metrics { return prometheusMetrics{} }

func (prometheusMetrics) ClientConnected() {
	connectedClientsGauge.Inc()
	clientsTotal.Inc()
}

func (prometheusMetrics) ClientDisconnected() {
	connectedClientsGauge.Dec()
	clientsDisconnectedTotal.Inc()
}

func (prometheusMetrics) ClientDropped() { clientsDroppedTotal.Inc() }

func (prometheusMetrics) Broadcast() { broadcastTotal.Inc() }
