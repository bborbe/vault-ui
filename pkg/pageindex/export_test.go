// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex

import dto "github.com/prometheus/client_model/go"

// FilesReadTotal returns the current value of one files-read counter series.
// The counter is package-global, so callers must assert deltas, not absolutes.
func FilesReadTotal(reason string) float64 {
	metric := &dto.Metric{}
	if err := filesReadTotal.WithLabelValues(reason).Write(metric); err != nil {
		return -1
	}
	return metric.GetCounter().GetValue()
}
