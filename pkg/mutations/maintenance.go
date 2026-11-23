// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mutations

import (
	"context"

	"github.com/bborbe/vault-ui/pkg/api"
)

// ReloadConfig re-reads the config and reports the vault and watcher names.
//
// The watcher set is supplied by WatcherNames — the same source the Go
// watcher supervisor (prompt 3) uses — so this endpoint and the supervisor
// cannot disagree about which vaults are watched.
func (s *service) ReloadConfig(ctx context.Context) (api.ConfigReloadResponse, error) {
	cfg, err := s.deps.Config.Load(ctx)
	if err != nil {
		return api.ConfigReloadResponse{}, newHTTPError(500, err.Error())
	}
	vaults := make([]string, 0, len(cfg.Vaults))
	for _, vault := range cfg.Vaults {
		vaults = append(vaults, vault.Name)
	}
	watchers := []string{}
	if s.deps.WatcherNames != nil {
		watchers = s.deps.WatcherNames(ctx)
	}
	return api.ConfigReloadResponse{Vaults: vaults, Watchers: watchers}, nil
}
