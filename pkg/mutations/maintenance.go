// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mutations

import (
	"context"

	"github.com/bborbe/vault-ui/pkg/api"
)

// ReloadCache forces a status-cache reload for one vault, or all vaults when
// vault is empty.
func (s *service) ReloadCache(ctx context.Context, vault string) (api.CacheReloadResponse, error) {
	cfg, err := s.deps.Config.Load(ctx)
	if err != nil {
		return api.CacheReloadResponse{}, err
	}
	if vault != "" {
		resolved, ok, resolveErr := s.vaultByName(ctx, vault)
		if resolveErr != nil {
			return api.CacheReloadResponse{}, resolveErr
		}
		if !ok {
			return api.CacheReloadResponse{}, newHTTPError(404, unknownVault(vault))
		}
		if loadErr := s.deps.Cache.LoadVault(
			resolved.Name, resolved.Path, resolved.TasksFolder,
		); loadErr != nil {
			return api.CacheReloadResponse{}, loadErr
		}
		return api.CacheReloadResponse{
			Reloaded: []string{resolved.Name},
			Counts:   map[string]int{resolved.Name: s.deps.Cache.Count(resolved.Name)},
		}, nil
	}
	reloaded := make([]string, 0, len(cfg.Vaults))
	counts := make(map[string]int, len(cfg.Vaults))
	for _, resolved := range cfg.Vaults {
		if loadErr := s.deps.Cache.LoadVault(
			resolved.Name, resolved.Path, resolved.TasksFolder,
		); loadErr != nil {
			return api.CacheReloadResponse{}, loadErr
		}
		reloaded = append(reloaded, resolved.Name)
		counts[resolved.Name] = s.deps.Cache.Count(resolved.Name)
	}
	return api.CacheReloadResponse{Reloaded: reloaded, Counts: counts}, nil
}

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
