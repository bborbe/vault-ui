// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package panecache keeps the board's WezTerm pane links off the request path.
//
// Resolving a live session's pane shells out to the supervisor's who-needs-me.py,
// which costs one helper subprocess per live session per request. The cache holds
// the last resolved pane map so the board can read a pane link with a pure map
// lookup, while a background refresher keeps that map current for every live
// session. A session's pane link is therefore at most one refresh interval out of
// date, and serving the board spawns no subprocess at all.
package panecache

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/bborbe/errors"
	"github.com/bborbe/run"
	"github.com/golang/glog"
)

// DefaultRefreshInterval is how often the refresher rebuilds the pane map.
const DefaultRefreshInterval = 3 * time.Second

// Resolver resolves a live session id to its WezTerm pane id.
type Resolver interface {
	Resolve(ctx context.Context, sessionID string) (string, bool)
}

// Cache is the in-memory pane map the board reads from. Its Resolve is a pure
// map lookup and never calls a Resolver or spawns a process.
type Cache interface {
	// Resolve returns the cached pane id for a session id.
	Resolve(ctx context.Context, sessionID string) (string, bool)
	// Replace atomically swaps the whole resolved map.
	Replace(resolved map[string]string)
}

type cache struct {
	mu    sync.RWMutex
	panes map[string]string
}

// NewCache creates an empty pane cache.
func NewCache() Cache {
	return &cache{panes: map[string]string{}}
}

func (c *cache) Resolve(_ context.Context, sessionID string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	paneID, ok := c.panes[sessionID]
	return paneID, ok
}

func (c *cache) Replace(resolved map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.panes = resolved
}

// RefreshParams carries every injectable dependency and the refresh interval.
type RefreshParams struct {
	Cache          Cache
	Resolver       Resolver
	LiveSessionIDs func(ctx context.Context) []string
	Interval       time.Duration
}

// Refresher rebuilds the pane cache from the live session ids.
type Refresher interface {
	// Refresh runs one pass and returns the number of panes resolved.
	Refresh(ctx context.Context) (int, error)
	// RunLoop runs Refresh once, then every Interval until ctx is cancelled.
	RunLoop(ctx context.Context) error
}

type refresher struct {
	cache          Cache
	resolver       Resolver
	liveSessionIDs func(ctx context.Context) []string
	interval       time.Duration
}

// NewRefresher creates a Refresher from the given params. A non-positive
// interval falls back to DefaultRefreshInterval.
func NewRefresher(params RefreshParams) Refresher {
	interval := params.Interval
	if interval <= 0 {
		interval = DefaultRefreshInterval
	}
	return &refresher{
		cache:          params.Cache,
		resolver:       params.Resolver,
		liveSessionIDs: params.LiveSessionIDs,
		interval:       interval,
	}
}

// Refresh runs one pass: it collects the live session ids, dedupes and sorts
// them, resolves each in parallel, and swaps the resolved map into the cache. A
// session that fails to resolve is logged and skipped, so it can never abort the
// pass or empty the cache for the other sessions. A cancelled context returns
// without replacing the cache.
func (r *refresher) Refresh(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, nil
	}

	ids := r.sortedUniqueLiveIDs(ctx)

	resolved := map[string]string{}
	var mu sync.Mutex
	funcs := make([]run.Func, 0, len(ids))
	for _, sessionID := range ids {
		sessionID := sessionID
		funcs = append(funcs, func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			paneID, ok := r.resolver.Resolve(ctx, sessionID)
			if !ok {
				glog.V(3).Infof("[PaneCache] no pane for session %s", sessionID)
				return nil
			}
			mu.Lock()
			resolved[sessionID] = paneID
			mu.Unlock()
			return nil
		})
	}

	if err := run.All(ctx, funcs...); err != nil {
		return 0, errors.Wrap(ctx, err, "refresh panes")
	}
	if err := ctx.Err(); err != nil {
		return 0, nil
	}

	r.cache.Replace(resolved)
	glog.V(3).Infof("[PaneCache] refreshed %d pane(s) for %d live session(s)", len(resolved), len(ids))
	return len(resolved), nil
}

// RunLoop runs Refresh once immediately and then every interval until ctx is
// cancelled. A pass error is logged and the loop continues.
func (r *refresher) RunLoop(ctx context.Context) error {
	glog.Infof("[PaneCache] Starting pane refresh loop")
	for {
		if _, err := r.Refresh(ctx); err != nil {
			if ctx.Err() != nil {
				glog.Infof("[PaneCache] Pane refresh loop cancelled")
				return nil
			}
			glog.Errorf("[PaneCache] Unexpected error in pane refresh: %v", err)
		}
		timer := time.NewTimer(r.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			glog.Infof("[PaneCache] Pane refresh loop cancelled during sleep")
			return nil
		case <-timer.C:
		}
	}
}

// sortedUniqueLiveIDs returns the live session ids, deduped and sorted.
func (r *refresher) sortedUniqueLiveIDs(ctx context.Context) []string {
	if r.liveSessionIDs == nil {
		return nil
	}
	ids := r.liveSessionIDs(ctx)
	seen := make(map[string]bool, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	sort.Strings(unique)
	return unique
}
