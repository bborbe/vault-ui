// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package sigterm delivers SIGTERM to a process. It is the only place the Go
// port sends a signal, kept deliberately small and separate from the guard
// logic in pkg/terminate so the one irreversible action is trivially
// reviewable. It reproduces _sigterm_pid from src/vault_ui/activity.py.
package sigterm

import (
	"context"
	stderrors "errors"
	"os"
	"syscall"

	"github.com/golang/glog"
)

// Signaler delivers SIGTERM to a pid. It is injectable so tests can spy on the
// exact pid and simulate failures; production uses NewProcessSignaler.
type Signaler interface {
	Signal(pid int) error
}

// SigtermPID sends SIGTERM to pid via signaler. It returns true when the signal
// was delivered and false for every expected failure:
//   - the process is already gone (the error wraps os.ErrProcessDone) — a debug
//     log line only, never a warning and never a raised error;
//   - any other failure (e.g. permission denied, syscall.EACCES) — a WARNING log
//     line naming the pid, the sessionID, and the error.
//
// It never panics and never returns an error: a failed termination is reported
// as false, not raised.
func SigtermPID(ctx context.Context, signaler Signaler, pid int, sessionID string) bool {
	if err := signaler.Signal(pid); err != nil {
		if stderrors.Is(err, os.ErrProcessDone) {
			// The process died between the ps scan and the kill — nothing to
			// terminate. The Python path is silent here; the debug line is a
			// documented addition for observability.
			glog.V(3).Infof("[Sigterm] Process %d for session %s already gone", pid, sessionID)
			return false
		}
		glog.Warningf("[Sigterm] Cannot SIGTERM pid %d for session %s: %v", pid, sessionID, err)
		return false
	}
	return true
}

// NewProcessSignaler returns a Signaler backed by the real process table.
func NewProcessSignaler() Signaler {
	return &processSignaler{}
}

type processSignaler struct{}

// Signal resolves pid against the real process table and sends SIGTERM. On Unix
// the returned error wraps os.ErrProcessDone for a dead pid and is a
// permission error (syscall.EACCES) for a foreign pid.
func (s *processSignaler) Signal(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(syscall.SIGTERM)
}
