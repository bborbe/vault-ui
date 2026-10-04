// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cleanup

import (
	"context"
	"time"

	libtime "github.com/bborbe/time"
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/terminate"
)

// markerTimeLayouts are the layouts accepted for a claude_session_started
// marker. RFC3339Nano covers the ISO-8601 instant with an offset that the launch
// path writes; the zone-less layouts cover a naive timestamp, which Python
// assumes is UTC.
var markerTimeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
}

// markerAgeSeconds returns the age in seconds of a claude_session_started marker
// and whether the marker carried a parseable instant. It returns ok=false only
// when the marker carries no parseable instant; that covers the legacy literal
// "true" written before 2026-08-29, and callers treat an unknown age as expired.
func markerAgeSeconds(marker string, now libtime.DateTime) (float64, bool) {
	started, ok := parseMarkerTime(marker)
	if !ok {
		return 0, false
	}
	age := now.UTC().Sub(libtime.DateTime(started.UTC())).Duration()
	return age.Seconds(), true
}

// parseMarkerTime parses a marker into a time.Time. A naive timestamp is
// interpreted as UTC, exactly as Python's datetime.replace(tzinfo=UTC) does.
func parseMarkerTime(marker string) (time.Time, bool) {
	for _, layout := range markerTimeLayouts {
		if parsed, err := time.Parse(layout, marker); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// ReconcileOrphanedMarkers clears claude_session_started markers whose launch
// this host no longer has. It runs once at startup: a restart kills the launches
// — they are this server's subprocesses — and the coroutines that would have
// cleared their markers die with it, so the cards sit on "Starting…" until the
// 45-minute TTL sweep. This closes that window to seconds: a marker is cleared
// when the registry has no record for the item, the marker is past the grace
// period, and no --session-id launch process for the item exists on this host.
//
// The trade-off is the shared-vault case: a marker written by a peer machine
// also has no local launch process, so a peer's in-flight turn can be cleared
// early here (the TTL sweep would clear it at 45 minutes anyway). Only the
// display is affected — claude_session_id is untouched.
func (s *sweep) ReconcileOrphanedMarkers(ctx context.Context) (int, error) {
	cleared := 0
	for _, vault := range s.params.Vaults {
		if err := ctx.Err(); err != nil {
			return cleared, nil
		}
		ops := s.params.OpsFor(vault)
		tasks, err := s.listTasks(ctx, ops)
		if err != nil {
			glog.Warningf(
				"[Cleanup] Cannot list tasks in vault %s for orphan reconciliation: %v",
				vault.Name,
				err,
			)
			continue
		}

		for _, task := range tasks {
			if ctxDone(ctx) {
				return cleared, nil
			}
			marker := task.ClaudeSessionStarted
			if marker == "" {
				continue
			}
			if _, known := s.params.LaunchRegistry.State(vault.Name, task.ID); known {
				continue // this process knows the launch — leave it alone
			}
			age, parsed := markerAgeSeconds(marker, s.params.Now)
			if parsed && age < s.params.OrphanGrace.Seconds() {
				continue // the launch may still be booting
			}
			if terminate.ItemHasLiveLaunch(
				ctx,
				s.params.ProcessScanner,
				task.ClaudeSessionID,
				task.Title,
			) {
				continue // a launch for this item is running here
			}

			if err := s.bounded(ctx, func(callCtx context.Context) error {
				return ops.ClearTaskField(callCtx, task.ID, "claude_session_started")
			}); err != nil {
				glog.Warningf(
					"[Cleanup] Failed to clear orphaned marker for task %s in vault %s: %v",
					task.ID,
					vault.Name,
					err,
				)
				continue
			}

			s.params.LaunchRegistry.Finish(vault.Name, task.ID)
			cleared++
			glog.Infof(
				"[Cleanup] Cleared orphaned claude_session_started marker for task %s in vault %s"+
					" (no launch process on this host)",
				task.ID,
				vault.Name,
			)
		}
	}
	return cleared, nil
}
