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

// changedPagesUnboundedTotal counts the times the page index refused to bound a
// changed set, by the cause of the refusal. Its totals are counts of refusals
// by cause, never of revision advances.
var changedPagesUnboundedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "vault_ui",
	Subsystem: "page_index",
	Name:      "changed_pages_unbounded_total",
	Help:      "Total changed-page sets the page index could not bound, by reason.",
}, []string{"reason"})

// The reasons a changed set cannot be bounded.
const (
	unboundedUnknownKey                   = "unknown_key"
	unboundedRevisionAhead                = "revision_ahead"
	unboundedWindowNotRetained            = "window_not_retained"
	unboundedForcedReload                 = "forced_reload"
	unboundedFirstPublication             = "first_publication"
	unboundedFolderMarkExplicit           = "folder_mark_explicit"
	unboundedFolderMarkWidenedName        = "folder_mark_widened_name_not_in_snapshot"
	unboundedFolderMarkWidenedFile        = "folder_mark_widened_file_absent"
	unboundedFolderMarkWidenedNameInvalid = "folder_mark_widened_invalid_name"
	unboundedFolderMarkWidenedKey         = "folder_mark_widened_key_unpublished"
)

// unboundedReasons is every series the counter carries.
var unboundedReasons = []string{
	unboundedUnknownKey,
	unboundedRevisionAhead,
	unboundedWindowNotRetained,
	unboundedForcedReload,
	unboundedFirstPublication,
	unboundedFolderMarkExplicit,
	unboundedFolderMarkWidenedName,
	unboundedFolderMarkWidenedFile,
	unboundedFolderMarkWidenedNameInvalid,
	unboundedFolderMarkWidenedKey,
}

func init() {
	prometheus.MustRegister(filesReadTotal, changedPagesUnboundedTotal)
	// Pre-initialize the series so alerting rules see them from boot.
	for _, reason := range readReasons {
		filesReadTotal.WithLabelValues(reason).Add(0)
	}
	for _, reason := range unboundedReasons {
		changedPagesUnboundedTotal.WithLabelValues(reason).Add(0)
	}
}

// recordRead counts one single-file page read.
func recordRead(reason string) { filesReadTotal.WithLabelValues(reason).Inc() }

// recordUnbounded counts one refused bound by its cause.
func recordUnbounded(reason string) { changedPagesUnboundedTotal.WithLabelValues(reason).Inc() }
