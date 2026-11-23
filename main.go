// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"os"

	"github.com/bborbe/errors"
	"github.com/bborbe/run"
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/fdlimit"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/sessionlock"
	"github.com/bborbe/vault-ui/pkg/sessionstate"
	"github.com/bborbe/vault-ui/pkg/vaultapi"
)

// The admin port defaults to :9090 and is overridable through
// VAULT_UI_ADMIN_LISTEN (see factory.CreateAdminListen), which is what lets
// `make e2e` bind its own port instead of stopping the live board.

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

	paneResolver := factory.CreatePaneResolver(homeDir)
	launches := launchregistry.NewRegistry()
	// One session-lock registry for the process: the API's set_task_session and
	// the cleanup sweep's re-bind write must share it to share the critical
	// section.
	locks := sessionlock.NewRegistry()
	manager := factory.CreateConnectionManager()
	sessionState := factory.CreateSessionState()
	sessionSnapshot := factory.CreateSessionSnapshot(sessionState)
	heartbeatStore := factory.CreateHeartbeatStore()
	writeQueue := factory.CreateWriteQueue()
	vaultSource := vaultapi.NewSource(vaultapi.New(vaultapi.ConfigFromEnv()))
	vaultSource.SetOnChange(factory.CreateSourceChangeBroadcaster(manager))
	apiHandler := factory.CreateAPIHandler(
		loader, configPath, paneResolver, launches, locks, homeDir, readiness, manager,
		sessionSnapshot, writeQueue, vaultSource,
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
		vaultSource.Run,
		factory.CreateSessionStateWatcher(
			loader, manager, sessionState, heartbeatStore, sessionstate.DefaultRescanInterval,
		),
		sessionSnapshot.Run,
		factory.CreateCleanupSweep(
			loader, configPath, launches, locks, homeDir,
		),
		writeQueue.Consume,
		factory.CreateHTTPServer(factory.CreateAdminListen(), readiness),
		factory.CreateAPIServer(factory.CreateAPIListen(), apiHandler),
	); err != nil {
		return errors.Wrap(ctx, err, "run failed")
	}
	return nil
}
