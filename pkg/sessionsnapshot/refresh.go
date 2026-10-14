// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sessionsnapshot

import (
	"context"

	libtime "github.com/bborbe/time"
)

// Run refreshes once, then once per SessionRefreshInterval until ctx is done,
// but only while the snapshot is in demand: a tick that finds it unread since
// the previous refresh performs no work, so an idle board spawns no `ps` child.
// A failed refresh is logged by RefreshOnce and the loop continues: the next
// tick with demand retries, and the previous values stay served in the
// meantime.
//
// The wait goes through the injected libtime.WaiterDuration, so there is no
// ticker and no configurable interval — SessionRefreshInterval is the only
// period, and it is a ceiling rather than a schedule.
func (s *snapshot) Run(ctx context.Context) error {
	// The startup refresh is unconditional: the snapshot must have values before
	// the first read, and it is the one refresh an idle board ever performs.
	_ = s.RefreshOnce(ctx)

	for {
		if err := s.params.Waiter.Wait(ctx, libtime.Duration(SessionRefreshInterval)); err != nil {
			return nil
		}
		if !s.demanded() {
			// No read since the last refresh: this tick does no work. The 60 s
			// cadence is a ceiling, not a schedule.
			continue
		}
		_ = s.RefreshOnce(ctx)
	}
}
