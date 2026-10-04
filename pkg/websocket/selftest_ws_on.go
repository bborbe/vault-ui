// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build parity_selftest_ws_frame

package websocket

// selftestWatcherEvent deliberately corrupts the watcher frame's type in the
// parity harness self-test build, so the harness must detect a divergent
// WebSocket frame and fail loudly.
func selftestWatcherEvent(string) string { return "modified_x" }
