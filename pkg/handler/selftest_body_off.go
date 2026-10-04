// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !parity_selftest_body_diverge

package handler

import (
	"net/http"

	"github.com/bborbe/vault-ui/pkg/api"
)

// selftestDivergeVaultsResponse is a no-op in a normal build.
func selftestDivergeVaultsResponse(_ http.ResponseWriter, _ []api.VaultResponse) bool {
	return false
}
