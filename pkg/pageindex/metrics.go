// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex

import "github.com/prometheus/client_golang/prometheus"

// filesReadTotal counts single-file page reads, so the cost of keeping the
// index current is visible next to the folder-wide parse it replaced.
var filesReadTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "vault_ui",
	Subsystem: "page_index",
	Name:      "files_read_total",
	Help:      "Total single-file page reads, by reason.",
}, []string{"reason"})

// The reasons a single-file read happens.
const (
	reasonBuild  = "build"
	reasonEvent  = "event"
	reasonWrite  = "write"
	reasonRescan = "rescan"
	reasonReload = "reload"
)

// readReasons is every series the counter carries.
var readReasons = []string{reasonBuild, reasonEvent, reasonWrite, reasonRescan, reasonReload}

func init() {
	prometheus.MustRegister(filesReadTotal)
	// Pre-initialize the series so alerting rules see them from boot.
	for _, reason := range readReasons {
		filesReadTotal.WithLabelValues(reason).Add(0)
	}
}

// recordRead counts one single-file page read.
func recordRead(reason string) { filesReadTotal.WithLabelValues(reason).Inc() }
