// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build parity_selftest_skip_write

package mutations

// selftestSkipFlagWrite deliberately skips a vault-file write in the parity
// harness self-test build, so the harness must detect the resulting file diff
// and fail loudly.
func selftestSkipFlagWrite() bool { return true }
