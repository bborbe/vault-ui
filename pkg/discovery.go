// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultui

import (
	"context"

	"github.com/bborbe/errors"
	"github.com/bborbe/vault-cli/pkg/config"
)

// DiscoverVaults loads every vault from vault-cli's config and, on success,
// marks readiness ready. It is the service's startup discovery step and the
// only place vault paths are resolved — vault-ui never resolves them itself.
func DiscoverVaults(ctx context.Context, loader config.Loader, readiness Readiness) error {
	if _, err := loader.GetAllVaults(ctx); err != nil {
		return errors.Wrap(ctx, err, "discover vaults")
	}
	readiness.SetReady()
	return nil
}
