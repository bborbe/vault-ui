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
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/pageindex"
	"github.com/bborbe/vault-ui/pkg/vaultconfig"
)

// CreatePageIndex returns the process-wide page index over the reader and
// lister seams.
func CreatePageIndex(
	reader pageindex.PageReader,
	lister pageindex.DirectoryLister,
	currentDateTimeGetter libtime.CurrentDateTimeGetter,
) pageindex.PageIndex {
	return pageindex.NewPageIndex(
		reader,
		lister,
		currentDateTimeGetter,
		libtime.NewWaiterDuration(),
	)
}

// CreatePageIndexStore opens the process-wide page-index store at its fixed
// location under the user cache directory. It never fails: an unusable location
// yields a store that serves nothing and reports a discard.
func CreatePageIndexStore(ctx context.Context) pageindex.Store {
	return pageindex.OpenDefaultStore(ctx, glog.Warningf, func(format string, args ...any) {
		glog.V(2).Infof(format, args...)
	})
}

// CreatePageIndexWithStore returns the process-wide page index over the reader
// and lister seams, hydrated from store.
func CreatePageIndexWithStore(
	reader pageindex.PageReader,
	lister pageindex.DirectoryLister,
	currentDateTimeGetter libtime.CurrentDateTimeGetter,
	store pageindex.Store,
) pageindex.PageIndex {
	return pageindex.NewPageIndexWithStore(
		reader,
		lister,
		currentDateTimeGetter,
		libtime.NewWaiterDuration(),
		store,
	)
}

// CreatePageIndexWarmup returns a run.Func that builds every configured vault's
// tasks, goals and topics folders concurrently once at startup.
func CreatePageIndexWarmup(
	loader config.Loader,
	configPath string,
	pageIndex pageindex.PageIndex,
) run.Func {
	return func(ctx context.Context) error {
		cfg, err := vaultconfig.Load(ctx, loader, configPath)
		if err != nil {
			return errors.Wrap(ctx, err, "load vault config")
		}
		if err := pageIndex.Build(ctx, pageIndexKeys(cfg.Vaults)); err != nil {
			if ctx.Err() != nil {
				// Shutdown is not a failure, matching CreateWatcher.
				return nil
			}
			return errors.Wrap(ctx, err, "build page index")
		}
		return nil
	}
}

// pageIndexKeys derives the index keys for the configured vaults: each vault's
// tasks and goals folder, plus its topics folder when one is configured.
func pageIndexKeys(vaults []vaultconfig.Vault) []pageindex.Key {
	keys := make([]pageindex.Key, 0, len(vaults)*3)
	for _, vault := range vaults {
		keys = append(
			keys,
			pageindex.NewKey(vault.Path, vault.TasksFolder),
			pageindex.NewKey(vault.Path, vault.GoalsFolder),
		)
		if vault.TopicsFolder != "" {
			keys = append(keys, pageindex.NewKey(vault.Path, vault.TopicsFolder))
		}
	}
	return keys
}
