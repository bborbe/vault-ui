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
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/fdlimit"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/pageindex"
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
	manager := factory.CreateConnectionManager()
	// The process-wide page index reads single page files through the
	// production reader and lister seams, shared by every vault.
	pageIndex := factory.CreatePageIndex(
		pageindex.NewPageReader(),
		pageindex.NewDirectoryLister(),
		libtime.NewCurrentDateTime(),
	)
	sessionState := factory.CreateSessionState()
	sessionSnapshot := factory.CreateSessionSnapshot(sessionState)
	writeQueue := factory.CreateWriteQueue()
	apiHandler := factory.CreateAPIHandler(
		loader, configPath, cache, paneResolver, launches, homeDir, readiness, manager, pageIndex,
		sessionSnapshot, writeQueue,
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
		factory.CreateStatusCacheLoader(loader, configPath, cache),
		factory.CreatePageIndexWarmup(loader, configPath, pageIndex),
		pageIndex.Rescan,
		factory.CreateWatcher(loader, manager, pageIndex, ops.NewWatchOperation()),
		factory.CreateSessionStateWatcher(loader, manager, sessionState, homeDir),
		sessionSnapshot.Run,
		writeQueue.Consume,
		factory.CreateHTTPServer(adminListen, readiness),
		factory.CreateAPIServer(factory.CreateAPIListen(), apiHandler),
	); err != nil {
		return errors.Wrap(ctx, err, "run failed")
	}
	return nil
}
