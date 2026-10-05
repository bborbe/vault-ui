// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package watchrefresh turns vault-cli watcher events into page-index
// refreshes and WebSocket frames.
//
// It is the event side of the page index: a task or goal event refreshes the
// single folder it names, and the event's frame is broadcast only after that
// refresh has swapped in a snapshot built after the event, so a client that
// re-fetches on the frame sees fresh data. Theme and objective events, and
// events for a vault the service does not know, are broadcast unchanged
// without touching the index. The staleness, frame-ordering and key-derivation
// rules are written down in docs/page-index.md.
package watchrefresh

import (
	"context"

	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/pageindex"
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

// NewHandler returns the watch handler: it refreshes the event's key, then
// broadcasts the event's frame. It builds the vault lookup once; the returned
// handler holds no mutable state and is safe to run concurrently, which the
// debouncer requires — a new event for a path whose previous handler is still
// blocked starts a second handler at the same time.
func NewHandler(
	ctx context.Context,
	vaults []*config.Vault,
	pageIndex pageindex.PageIndex,
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
			if err := pageIndex.Refresh(ctx, key); err != nil {
				// Refresh returns an error only when ctx was cancelled first:
				// shutdown. The rebuild itself keeps the previous snapshot and
				// logs its own failure, so there is nothing to surface here.
				glog.V(2).Infof("drop frame on shutdown for %s/%s", event.Vault, event.Name)
				return nil
			}
		}
		manager.Broadcast(websocket.WatcherFrame(event))
		return nil
	}
}
