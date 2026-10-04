// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !parity_selftest_ws_frame

package websocket

// selftestWatcherEvent is the identity function in a normal build.
func selftestWatcherEvent(event string) string { return event }
