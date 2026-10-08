// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex

import (
	"fmt"

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

// NewPageIndexWithStoreAndWarnf builds a store-backed page index whose warnings
// go to warnf instead of glog, so a test can capture them. It must be used
// before the index reads anything.
func NewPageIndexWithStoreAndWarnf(
	reader PageReader,
	lister DirectoryLister,
	currentDateTimeGetter libtime.CurrentDateTimeGetter,
	waiter libtime.WaiterDuration,
	store Store,
	warnf func(format string, args ...any),
) PageIndex {
	index := NewPageIndexWithStore(
		reader, lister, currentDateTimeGetter, waiter, store,
	).(*pageIndex)
	index.warnf = warnf
	return index
}

// FingerprintSetIdentity returns an identity token for the key's recorded
// fingerprint set, so a test can assert a pass did not replace it. An unknown
// key reports "".
func FingerprintSetIdentity(index PageIndex, key Key) string {
	pi, ok := index.(*pageIndex)
	if !ok {
		return ""
	}
	pi.mu.Lock()
	defer pi.mu.Unlock()
	e, ok := pi.entries[key]
	if !ok {
		return ""
	}
	return fmt.Sprintf("%p", e.fingerprints)
}

// SnapshotIdentity returns an identity token for the key's published snapshot
// slice, so a test can assert a pass did not replace it. An unknown key, or a
// key with no published snapshot, reports "".
func SnapshotIdentity(index PageIndex, key Key) string {
	pi, ok := index.(*pageIndex)
	if !ok {
		return ""
	}
	pi.mu.Lock()
	defer pi.mu.Unlock()
	e, ok := pi.entries[key]
	if !ok || !e.hasSnapshot {
		return ""
	}
	return fmt.Sprintf("%p", e.snapshot)
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
