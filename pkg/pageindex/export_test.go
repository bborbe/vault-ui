// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex

import (
	libtime "github.com/bborbe/time"
	dto "github.com/prometheus/client_model/go"
)

// NewPageIndexWithWarnf builds a page index whose per-file exclusion warnings
// go to warnf instead of glog, so a test can capture the once-per-fingerprint
// warnings. It must be used before the index reads anything.
func NewPageIndexWithWarnf(
	reader PageReader,
	lister DirectoryLister,
	currentDateTimeGetter libtime.CurrentDateTimeGetter,
	waiter libtime.WaiterDuration,
	warnf func(format string, args ...any),
) PageIndex {
	index := NewPageIndex(reader, lister, currentDateTimeGetter, waiter).(*pageIndex)
	index.warnf = warnf
	return index
}

// FilesReadTotal returns the current value of one files-read counter series.
// The counter is package-global, so callers must assert deltas, not absolutes.
func FilesReadTotal(reason string) float64 {
	metric := &dto.Metric{}
	if err := filesReadTotal.WithLabelValues(reason).Write(metric); err != nil {
		return -1
	}
	return metric.GetCounter().GetValue()
}
