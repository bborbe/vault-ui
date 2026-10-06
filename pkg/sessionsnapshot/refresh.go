// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sessionsnapshot

import (
	"context"

	libtime "github.com/bborbe/time"
)

// Run refreshes once, then once per SessionRefreshInterval until ctx is done.
// A failed refresh is logged by RefreshOnce and the loop continues: the next
// tick retries, and the previous values stay served in the meantime.
//
// The wait goes through the injected libtime.WaiterDuration, so there is no
// ticker and no configurable interval — SessionRefreshInterval is the only
// period.
func (s *snapshot) Run(ctx context.Context) error {
	// A failure here is already logged with the interval and the cause; the
	// loop below retries on the next tick.
	_ = s.RefreshOnce(ctx)

	for {
		if err := s.params.Waiter.Wait(ctx, libtime.Duration(SessionRefreshInterval)); err != nil {
			return nil
		}
		_ = s.RefreshOnce(ctx)
	}
}
