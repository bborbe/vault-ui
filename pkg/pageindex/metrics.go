// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex

import (
	"github.com/bborbe/vault-cli/pkg/index"
	"github.com/prometheus/client_golang/prometheus"
)

// filesReadTotal counts single-file page reads, so the cost of keeping the
// index current is visible next to the folder-wide parse it replaced.
var filesReadTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "vault_ui",
	Subsystem: "page_index",
	Name:      "files_read_total",
	Help:      "Total single-file page reads, by reason.",
}, []string{"reason"})

// changedPagesUnboundedTotal counts the times the page index refused to bound a
// changed set, by the cause of the refusal. Its totals are counts of refusals
// by cause, never of revision advances.
var changedPagesUnboundedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "vault_ui",
	Subsystem: "page_index",
	Name:      "changed_pages_unbounded_total",
	Help:      "Total changed-page sets the page index could not bound, by reason.",
}, []string{"reason"})

func init() {
	prometheus.MustRegister(filesReadTotal, changedPagesUnboundedTotal)
	// Pre-initialize the series so alerting rules see them from boot.
	for _, reason := range index.ReadReasons {
		filesReadTotal.WithLabelValues(reason).Add(0)
	}
	for _, reason := range index.UnboundedReasons {
		changedPagesUnboundedTotal.WithLabelValues(reason).Add(0)
	}
}

// NewMetrics returns the index.Metrics adapter onto vault-ui's prometheus
// counters, keeping the pre-migration metric names.
func NewMetrics() index.Metrics {
	return prometheusMetrics{}
}

type prometheusMetrics struct{}

// FileRead counts one single-file page read.
func (prometheusMetrics) FileRead(reason string) {
	filesReadTotal.WithLabelValues(reason).Inc()
}

// ChangedPagesUnbounded counts one refused bound by its cause.
func (prometheusMetrics) ChangedPagesUnbounded(reason string) {
	changedPagesUnboundedTotal.WithLabelValues(reason).Inc()
}
