// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultui

import "sync/atomic"

// Readiness reports whether the service has completed startup. The admin
// /readiness endpoint serves 503 until SetReady has been called, then 200.
type Readiness interface {
	SetReady()
	IsReady() bool
}

type readiness struct {
	ready atomic.Bool
}

// NewReadiness returns a Readiness that starts not-ready.
func NewReadiness() Readiness {
	return &readiness{}
}

func (r *readiness) SetReady() {
	r.ready.Store(true)
}

func (r *readiness) IsReady() bool {
	return r.ready.Load()
}
