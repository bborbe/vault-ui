// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultui

import "github.com/prometheus/client_golang/prometheus"

var buildInfoGauge = prometheus.NewGauge(prometheus.GaugeOpts{
	Namespace: "vault_ui",
	Name:      "build_info",
	Help:      "Build information for the running vault-ui binary; always 1.",
})

func init() {
	prometheus.MustRegister(buildInfoGauge)
	buildInfoGauge.Set(1)
}
