// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board

import "github.com/prometheus/client_golang/prometheus"

// taskSnapshotRebuildsTotal counts task-list snapshot rebuilds by the kind of
// work the rebuild performed.
var taskSnapshotRebuildsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "vault_ui",
	Subsystem: "board",
	Name:      "task_snapshot_rebuilds_total",
	Help:      "Total task-list snapshot rebuilds, by kind.",
}, []string{"kind"})

// The kinds of work a task-list rebuild performs.
const (
	rebuildKindFull    = "full"
	rebuildKindPatch   = "patch"
	rebuildKindRefresh = "refresh"
)

// rebuildKinds is every series the rebuild-kind counter carries.
var rebuildKinds = []string{rebuildKindFull, rebuildKindPatch, rebuildKindRefresh}

// taskSnapshotFullRebuildsTotal counts full task-list snapshot rebuilds by the
// cause that forced the full build.
var taskSnapshotFullRebuildsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "vault_ui",
	Subsystem: "board",
	Name:      "task_snapshot_full_rebuilds_total",
	Help:      "Total task-list snapshot full rebuilds, by cause.",
}, []string{"reason"})

// The causes a full task-list rebuild is attributed to.
const (
	fullReasonColdKey             = "cold_key"
	fullReasonBothMoved           = "both_moved"
	fullReasonPatchNotWired       = "patch_not_wired"
	fullReasonChangedSetUnbounded = "changed_set_unbounded"
	fullReasonNewPageNoHeldRow    = "new_page_no_held_row"
	fullReasonPatchPageNotListed  = "patch_page_not_listed"
)

// fullRebuildReasons is every series the full-rebuild-cause counter carries.
var fullRebuildReasons = []string{
	fullReasonColdKey,
	fullReasonBothMoved,
	fullReasonPatchNotWired,
	fullReasonChangedSetUnbounded,
	fullReasonNewPageNoHeldRow,
	fullReasonPatchPageNotListed,
}

func init() {
	prometheus.MustRegister(taskSnapshotRebuildsTotal, taskSnapshotFullRebuildsTotal)
	// Pre-initialize the series so alerting rules see them from boot.
	for _, kind := range rebuildKinds {
		taskSnapshotRebuildsTotal.WithLabelValues(kind).Add(0)
	}
	for _, reason := range fullRebuildReasons {
		taskSnapshotFullRebuildsTotal.WithLabelValues(reason).Add(0)
	}
}

// recordRebuild counts one task-list rebuild by the kind of work it performed.
func recordRebuild(kind string) { taskSnapshotRebuildsTotal.WithLabelValues(kind).Inc() }

// recordFullRebuild counts one full task-list rebuild by its cause.
func recordFullRebuild(reason string) { taskSnapshotFullRebuildsTotal.WithLabelValues(reason).Inc() }
