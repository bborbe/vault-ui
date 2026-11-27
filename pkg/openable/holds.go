// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package openable

import (
	"context"
	"reflect"
	"sync"
	"time"

	libtime "github.com/bborbe/time"
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/api"
)

// DefaultInterval is the period between two openable reads — the "briefly
// cached" window a hold reason may lag the wrapper by.
const DefaultInterval = 20 * time.Second

// Lookup answers the board's per-row question: why does the wrapper hold this
// row? Generation advances whenever the held set changes, so a cached board
// body built from an older set is discarded.
type Lookup interface {
	// Hold returns the hold for (vault, kind, name), or nil when the wrapper does
	// not hold that row or cannot be reached.
	Hold(vault, kind, name string) *api.HoldResponse
	Generation() uint64
}

// Holds is a Lookup kept current by Run.
type Holds interface {
	Lookup
	// Refresh performs one read and reports whether the held set changed.
	Refresh(ctx context.Context) bool
	// Run refreshes once, then once per interval until ctx is cancelled. It never
	// returns an error: the wrapper being down only empties the held set.
	Run(ctx context.Context) error
}

// HoldsParams carries the store's dependencies.
type HoldsParams struct {
	Client Client
	Clock  libtime.CurrentDateTimeGetter
	// Location renders parked-until times; nil means time.Local.
	Location *time.Location
	// Interval is the poll period; non-positive falls back to DefaultInterval.
	Interval time.Duration
	// Changed, when set, is called after a refresh that changed the held set.
	Changed func()
}

// NewHolds returns an empty store; it holds nothing until the first Refresh.
func NewHolds(params HoldsParams) Holds {
	if params.Interval <= 0 {
		params.Interval = DefaultInterval
	}
	if params.Location == nil {
		params.Location = time.Local
	}
	return &holds{params: params, index: map[string]*api.HoldResponse{}}
}

type holds struct {
	params     HoldsParams
	mu         sync.RWMutex
	index      map[string]*api.HoldResponse
	generation uint64
}

// Key is the join key of one row: vault, kind and name.
func Key(vault, kind, name string) string {
	return vault + "\x00" + kind + "\x00" + name
}

func (h *holds) Hold(vault, kind, name string) *api.HoldResponse {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.index[Key(vault, kind, name)]
}

func (h *holds) Generation() uint64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.generation
}

func (h *holds) Refresh(ctx context.Context) bool {
	next := map[string]*api.HoldResponse{}
	rows, err := h.params.Client.Rows(ctx)
	if err != nil {
		// Fail-open: no wrapper, no reasons.
		glog.V(3).Infof("[Openable] cannot read claude-wrapper openable rows: %v", err)
	} else {
		now := h.params.Clock.Now().Time()
		for _, row := range rows {
			if hold := NewHold(row, now, h.params.Location); hold != nil {
				next[Key(row.Vault, row.Kind, row.Name)] = hold
			}
		}
	}

	h.mu.Lock()
	changed := !reflect.DeepEqual(h.index, next)
	if changed {
		h.index = next
		h.generation++
	}
	h.mu.Unlock()

	if changed && h.params.Changed != nil {
		h.params.Changed()
	}
	return changed
}

func (h *holds) Run(ctx context.Context) error {
	h.Refresh(ctx)
	timer := time.NewTimer(h.params.Interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			h.Refresh(ctx)
			timer.Reset(h.params.Interval)
		}
	}
}
