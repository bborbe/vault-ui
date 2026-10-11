// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package watchrefresh turns vault-cli watcher events into page-index
// refreshes and WebSocket frames.
//
// It is the event side of the page index: a task or goal event re-reads the
// single file the event names, and the event's frame is broadcast only after
// that read has swapped in a snapshot built after the event, so a client that
// re-fetches on the frame sees fresh data. An event whose path is not a single
// plain filename inside the key's folder falls back to the folder-level
// stat-diff. Once the refresh succeeds the handler also invalidates the changed
// item in the status cache, so a status change made outside the board is
// reflected without a restart. Theme and objective events, and events for a
// vault the service does not know, are broadcast unchanged without touching the
// index or the cache. The staleness, frame-ordering and key-derivation rules are
// written down in docs/page-index.md.
package watchrefresh

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/pageindex"
	"github.com/bborbe/vault-ui/pkg/statuscache"
	"github.com/bborbe/vault-ui/pkg/websocket"
)

// EventKey maps a watcher event to the page-index key it invalidates. It
// returns false for events that do not name an indexed folder: theme and
// objective events, and events whose vault is not in the lookup.
func EventKey(
	event ops.WatchEvent,
	vaultsByName map[string]*config.Vault,
) (pageindex.Key, bool) {
	vault, ok := vaultsByName[event.Vault]
	if !ok {
		return pageindex.Key{}, false
	}
	switch event.Type {
	case "task":
		return pageindex.NewKey(vault.Path, vault.GetTasksDir()), true
	case "goal":
		return pageindex.NewKey(vault.Path, vault.GetGoalsDir()), true
	default:
		return pageindex.Key{}, false
	}
}

// EventFilename returns the file's base name within the key's folder for a
// watcher event, or false when the event's path is not a single plain filename
// directly inside that folder. The name includes the ".md" suffix.
func EventFilename(event ops.WatchEvent, key pageindex.Key) (string, bool) {
	rel, err := filepath.Rel(key.PagesDir, event.Path)
	if err != nil || rel == "." || filepath.Base(rel) != rel ||
		strings.Contains(rel, "..") || !strings.HasSuffix(rel, ".md") {
		return "", false
	}
	return rel, true
}

// NewHandler returns the watch handler: it re-reads the event's single file
// (falling back to the folder-level stat-diff when the event's path is not a
// single plain filename inside the key's folder), invalidates the changed item
// in the status cache once that refresh succeeds, then broadcasts the event's
// frame. It builds the vault lookup once; the returned handler holds no mutable
// state and is safe to run concurrently, which the debouncer requires — a new
// event for a path whose previous handler is still blocked starts a second
// handler at the same time. Concurrency safety for the cache comes from the
// cache's own locking.
func NewHandler(
	ctx context.Context,
	vaults []*config.Vault,
	pageIndex pageindex.PageIndex,
	cache statuscache.Cache,
	manager websocket.ConnectionManager,
) func(ops.WatchEvent) error {
	vaultsByName := make(map[string]*config.Vault, len(vaults))
	for _, vault := range vaults {
		vaultsByName[vault.Name] = vault
	}
	return func(event ops.WatchEvent) error {
		glog.V(3).Infof("watcher event %s %s/%s", event.Event, event.Vault, event.Name)
		key, indexed := EventKey(event, vaultsByName)
		switch {
		case !indexed:
			if _, known := vaultsByName[event.Vault]; !known {
				glog.V(2).Infof(
					"watcher event for unknown vault %s, broadcasting without refresh",
					event.Vault,
				)
			}
		default:
			var err error
			if filename, ok := EventFilename(event, key); ok {
				err = pageIndex.RefreshFile(ctx, key, filename)
			} else {
				err = pageIndex.Refresh(ctx, key)
			}
			if err != nil {
				// RefreshFile pre-validates its filename and Refresh fails only
				// when ctx was cancelled first: shutdown. The read itself keeps
				// the previous snapshot and logs its own failure, so there is
				// nothing to surface here.
				glog.V(2).Infof("drop frame on shutdown for %s/%s", event.Vault, event.Name)
				return nil
			}
			// The index and the cache must agree on what is current: refresh
			// first, then drop the item's cached status so the next read
			// re-derives it from disk. The event's name already carries the
			// item id (base name without ".md").
			cache.Invalidate(event.Vault, event.Name)
		}
		manager.Broadcast(websocket.WatcherFrame(event))
		return nil
	}
}
