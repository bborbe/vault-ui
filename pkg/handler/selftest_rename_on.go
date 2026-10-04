// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build parity_selftest_rename_route

package handler

// vaultsRoutePath is deliberately wrong in the parity harness self-test build,
// so the harness must detect the missing route and fail loudly.
func vaultsRoutePath() string {
	return "/api/vaults-renamed"
}
