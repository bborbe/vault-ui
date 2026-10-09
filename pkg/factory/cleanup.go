// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	"context"

	"github.com/bborbe/errors"
	"github.com/bborbe/run"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/golang/glog"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/cleanup"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/session"
	"github.com/bborbe/vault-ui/pkg/sessionlock"
	"github.com/bborbe/vault-ui/pkg/sessionstate"
	"github.com/bborbe/vault-ui/pkg/statuscache"
	"github.com/bborbe/vault-ui/pkg/vaultconfig"
)

// CreateCleanupSweep returns a run.Func that runs the startup orphan
// reconciliation once and then the five-minute cleanup loop until ctx is
// cancelled.
//
// cacheReady is closed by CreateStatusCacheLoader once the status cache has been
// loaded. The startup reconcile reads the marker from that cache, so it waits on
// the channel: the loader sits in the same concurrent run group, and a reconcile
// that started alongside it would read an empty cache and clear nothing.
//
// cache, launches and locks must be the API's own instances. The race the sweep
// closes is observed through the same launch registry the launch path writes,
// and the re-bind write joins the API's per-item critical section.
func CreateCleanupSweep(
	loader config.Loader,
	configPath string,
	cache statuscache.Cache,
	launches launchregistry.Registry,
	locks sessionlock.Registry,
	homeDir string,
	cacheReady <-chan struct{},
) run.Func {
	return func(ctx context.Context) error {
		cfg, err := vaultconfig.Load(ctx, loader, configPath)
		if err != nil {
			return errors.Wrap(ctx, err, "load vault config for cleanup sweep")
		}
		sweep := cleanup.NewSweep(cleanupSweepParams(cfg, cache, launches, locks, homeDir))

		select {
		case <-cacheReady:
		case <-ctx.Done():
			return nil
		}

		cleared, reconcileErr := sweep.ReconcileOrphanedMarkers(ctx)
		if reconcileErr != nil {
			glog.Warningf("cleanup startup reconcile failed: %v", reconcileErr)
		} else {
			glog.V(2).Infof("cleanup startup reconcile cleared %d orphaned marker(s)", cleared)
		}
		return sweep.RunLoop(ctx)
	}
}

// cleanupSweepParams builds the sweep's params from the merged config and the
// API's shared instances. Every duration comes from the package's Default*
// constants — no flag, env var or config key exists for them.
func cleanupSweepParams(
	cfg *vaultconfig.Config,
	cache statuscache.Cache,
	launches launchregistry.Registry,
	locks sessionlock.Registry,
	homeDir string,
) cleanup.SweepParams {
	byName := make(map[string]vaultconfig.Vault, len(cfg.Vaults))
	vaults := make([]cleanup.Vault, 0, len(cfg.Vaults))
	for _, vault := range cfg.Vaults {
		byName[vault.Name] = vault
		vaults = append(vaults, cleanup.Vault{
			Name:              vault.Name,
			Path:              vault.Path,
			TasksFolder:       vault.TasksFolder,
			SessionProjectDir: vault.SessionProjectDir,
		})
	}

	// The process scanner is the same `ps -axww -o args=` read the rest of the
	// process uses; the table caches one scan per rescan interval.
	scanner := session.NewPSScanner("-axww", "-o", "args=")
	processTable := session.NewProcessTable(
		scanner, sessionstate.DefaultRescanInterval, libtime.NewCurrentDateTime(),
	)

	return cleanup.SweepParams{
		Vaults: vaults,
		OpsFor: func(vault cleanup.Vault) cleanup.VaultOps {
			return cleanupOpsFor(byName[vault.Name])
		},
		HomeDir:        homeDir,
		CurrentUser:    cfg.CurrentUser,
		LaunchRegistry: launches,
		SessionLock:    locks,
		StatusCache:    cache,
		LiveNames:      processTable.LiveSessionNames,
		ProcessScanner: scanner,
		// A clock, not a snapshot: RunLoop reuses one SweepParams for the whole
		// process lifetime, so a frozen instant would never clear a marker written
		// after startup.
		Now:                libtime.NewCurrentDateTime(),
		MarkerTTL:          cleanup.DefaultMarkerTTL,
		OrphanGrace:        cleanup.DefaultOrphanGrace,
		SetFieldTimeout:    cleanup.DefaultSetFieldTimeout,
		LockAcquireTimeout: cleanup.DefaultLockAcquireTimeout,
		CleanupInterval:    cleanup.DefaultCleanupInterval,
	}
}

// cleanupOpsFor builds the vault-cli ops the cleanup sweep needs for one vault.
func cleanupOpsFor(vault vaultconfig.Vault) cleanup.VaultOps {
	return newCleanupVaultOps(vaultCLIConfig(vault), mutationOpsFactory(vault))
}

// cleanupVaultOps adapts a vault's vault-cli OpSet to the cleanup sweep's
// VaultOps surface. Every read and write resolves the item's file through the
// ops, which take the vault's path and name — the sweep never reads or writes a
// vault file itself.
type cleanupVaultOps struct {
	vault *config.Vault
	set   vaultui.OpSet
}

// newCleanupVaultOps wraps an OpSet as the sweep's VaultOps.
func newCleanupVaultOps(vault *config.Vault, set vaultui.OpSet) cleanup.VaultOps {
	return &cleanupVaultOps{vault: vault, set: set}
}

func (o *cleanupVaultOps) ListTasks(ctx context.Context) ([]cleanup.Item, error) {
	items, err := o.set.List.Execute(
		ctx, o.vault.Path, o.vault.Name, o.vault.GetTasksDir(), nil, true, "", "",
	)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "list tasks")
	}
	return cleanupItems(items), nil
}

func (o *cleanupVaultOps) ListGoals(ctx context.Context) ([]cleanup.Item, error) {
	items, err := o.set.List.Execute(
		ctx, o.vault.Path, o.vault.Name, o.vault.GetGoalsDir(), nil, true, "", "",
	)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "list goals")
	}
	return cleanupItems(items), nil
}

func (o *cleanupVaultOps) ShowTask(ctx context.Context, itemID string) (cleanup.Item, error) {
	detail, err := o.set.Show.Execute(ctx, o.vault.Path, o.vault.Name, itemID)
	if err != nil {
		return cleanup.Item{}, errors.Wrap(ctx, err, "show task")
	}
	return cleanup.Item{
		ID:              detail.Name,
		Title:           detail.Name,
		Status:          detail.Status,
		Assignee:        detail.Assignee,
		ClaudeSessionID: detail.ClaudeSessionID,
	}, nil
}

func (o *cleanupVaultOps) SetTaskField(ctx context.Context, itemID, key, value string) error {
	return o.set.FrontmatterSet.Execute(
		ctx, o.vault.Path, itemID, key, value, "", "", "", false,
	)
}

func (o *cleanupVaultOps) ClearTaskField(ctx context.Context, itemID, key string) error {
	return o.set.FrontmatterClear.Execute(ctx, o.vault.Path, itemID, key)
}

func (o *cleanupVaultOps) SetGoalField(ctx context.Context, itemID, key, value string) error {
	return o.set.GoalSet.Execute(ctx, o.vault.Path, itemID, key, value, "", "")
}

func (o *cleanupVaultOps) ClearGoalField(ctx context.Context, itemID, key string) error {
	return o.set.GoalClear.Execute(ctx, o.vault.Path, itemID, key)
}

// cleanupItems maps vault-cli list items to the sweep's item view. The list item
// carries no title of its own; the board uses its name as the title, and the
// sweep's live-name lookup needs the same value.
func cleanupItems(items []ops.TaskListItem) []cleanup.Item {
	result := make([]cleanup.Item, 0, len(items))
	for _, item := range items {
		result = append(result, cleanup.Item{
			ID:              item.Name,
			Title:           item.Name,
			Status:          item.Status,
			Assignee:        item.Assignee,
			ClaudeSessionID: item.ClaudeSessionID,
		})
	}
	return result
}
