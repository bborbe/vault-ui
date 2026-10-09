// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"os"

	"github.com/bborbe/errors"
	"github.com/bborbe/run"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/bborbe/vault-cli/pkg/storage"
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/fdlimit"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/pageindex"
	"github.com/bborbe/vault-ui/pkg/sessionlock"
	"github.com/bborbe/vault-ui/pkg/sessionstate"
	"github.com/bborbe/vault-ui/pkg/statuscache"
)

// adminListen is the canonical bborbe admin port. It is deliberately NOT a
// flag or env var — spec 021 Non-goals forbid a port knob.
const adminListen = ":9090"

func main() {
	defer glog.Flush()
	ctx := run.ContextWithSig(context.Background())
	if err := execute(ctx); err != nil {
		glog.Errorf("vault-ui failed: %v", err)
		glog.Flush()
		os.Exit(1)
	}
}

func execute(ctx context.Context) error {
	readiness := factory.CreateReadiness()
	loader := factory.CreateConfigLoader("")

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return errors.Wrap(ctx, err, "resolve home dir")
	}
	configPath := factory.CreateVaultUIConfigPath(homeDir)

	cache := statuscache.NewCache()
	paneResolver := factory.CreatePaneResolver(homeDir)
	launches := launchregistry.NewRegistry()
	// One session-lock registry for the process: the API's set_task_session and
	// the cleanup sweep's re-bind write must share it to share the critical
	// section.
	locks := sessionlock.NewRegistry()
	// Closed by CreateStatusCacheLoader once the status cache is loaded; the
	// cleanup sweep's startup reconcile waits on it.
	cacheReady := make(chan struct{})
	manager := factory.CreateConnectionManager()
	// The process-wide page index reads single page files through the
	// production reader and lister seams, shared by every vault, and hydrates
	// from the on-disk store at its fixed cache path.
	pageIndex := factory.CreatePageIndexWithStore(
		pageindex.NewPageReader(storage.NewPageStorage(nil)),
		pageindex.NewDirectoryLister(),
		libtime.NewCurrentDateTime(),
		factory.CreatePageIndexStore(ctx),
	)
	sessionState := factory.CreateSessionState()
	sessionSnapshot := factory.CreateSessionSnapshot(sessionState)
	heartbeatStore := factory.CreateHeartbeatStore()
	writeQueue := factory.CreateWriteQueue()
	apiHandler := factory.CreateAPIHandler(
		loader, configPath, cache, paneResolver, launches, locks, homeDir, readiness, manager,
		pageIndex, sessionSnapshot, writeQueue,
	)

	limit, err := fdlimit.Raise(ctx)
	switch {
	case err == nil:
		// V(2) matches the sibling startup announcements in CreateWatcher and
		// CreateAPIServer.
		glog.V(2).Infof("file descriptor limit raised to %d", limit)
	case limit == fdlimit.UnknownLimit:
		// Nothing was applied and the current limit could not be read, so
		// naming a limit here would be a guess.
		glog.Warningf(
			"raise file descriptor limit failed: %v; vault watching may be incomplete",
			err,
		)
	default:
		glog.Warningf(
			"raise file descriptor limit failed: %v; applied limit %d, vault watching may be incomplete",
			err, limit,
		)
	}

	if err := run.CancelOnFirstErrorWait(ctx,
		factory.CreateVaultDiscovery(loader, readiness),
		factory.CreateStatusCacheLoader(loader, configPath, cache, cacheReady),
		factory.CreatePageIndexWarmup(loader, configPath, pageIndex),
		pageIndex.Rescan,
		factory.CreateWatcher(loader, manager, pageIndex, ops.NewWatchOperation()),
		factory.CreateSessionStateWatcher(
			loader, manager, sessionState, heartbeatStore, sessionstate.DefaultRescanInterval,
		),
		sessionSnapshot.Run,
		factory.CreateCleanupSweep(
			loader, configPath, cache, launches, locks, homeDir, cacheReady,
		),
		writeQueue.Consume,
		factory.CreateHTTPServer(adminListen, readiness),
		factory.CreateAPIServer(factory.CreateAPIListen(), apiHandler),
	); err != nil {
		return errors.Wrap(ctx, err, "run failed")
	}
	return nil
}
