// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package terminate holds the process-termination guards ported from
// src/vault_ui/activity.py. It resolves a target from a fresh (uncached)
// process scan immediately before signaling and never accepts a pid from the
// caller: only a claude row carrying an exact session-pinning flag for the
// target id is ever a target, and the take-over path signals a launch
// (--session-id) row while leaving an interactive resume (--resume) alone.
package terminate

import (
	"context"

	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/session"
	"github.com/bborbe/vault-ui/pkg/sigterm"
)

// TerminateResumedSession SIGTERMs the live claude process pinning sessionID
// (either --resume or --session-id). Returns true only when a matching process
// was found and signaled; false when no process matches.
func TerminateResumedSession(
	ctx context.Context,
	scan session.ProcessScanner,
	signaler sigterm.Signaler,
	sessionID string,
) bool {
	pid, ok := session.ParseLiveProcesses(freshScan(ctx, scan))[sessionID]
	if !ok {
		return false
	}
	return sigterm.SigtermPID(ctx, signaler, pid, sessionID)
}

// TerminateLaunchProcess SIGTERMs the in-flight LAUNCH process of a Starting card
// and returns (resolvedSessionID, terminated). Resolution is two-step: the card's
// own id when a live process pins it, else the `-n <itemName>` launch row. Only a
// --session-id (launch) row is ever signaled; a --resume (interactive resume) row
// pinning the same id is left alone. When nothing matches it returns
// (sessionID, false).
func TerminateLaunchProcess(
	ctx context.Context,
	scan session.ProcessScanner,
	signaler sigterm.Signaler,
	sessionID, itemName string,
) (string, bool) {
	ps := freshScan(ctx, scan)
	processes := session.ParseLaunchProcesses(ps)
	names := session.ParseLaunchNames(ps)

	resolved, found := resolveLaunchTarget(processes, names, sessionID, itemName)
	if !found {
		return sessionID, false
	}
	pid, ok := processes[resolved]
	if !ok {
		return resolved, false
	}
	return resolved, sigterm.SigtermPID(ctx, signaler, pid, resolved)
}

// ItemHasLiveLaunch reports whether a LAUNCH process (--session-id) for this item
// runs here. One fresh scan; interactive resumes never count.
func ItemHasLiveLaunch(
	ctx context.Context,
	scan session.ProcessScanner,
	sessionID, itemName string,
) bool {
	ps := freshScan(ctx, scan)
	if sessionID != "" {
		if _, ok := session.ParseLaunchProcesses(ps)[sessionID]; ok {
			return true
		}
	}
	_, ok := session.ParseLaunchNames(ps)[itemName]
	return ok
}

// resolveLaunchTarget reproduces the Python two-step resolution: the card's own
// id when a launch process pins it, else the `-n <itemName>` launch row.
func resolveLaunchTarget(
	processes map[string]int,
	names map[string]string,
	sessionID, itemName string,
) (string, bool) {
	if _, ok := processes[sessionID]; ok {
		return sessionID, true
	}
	if id, ok := names[itemName]; ok {
		return id, true
	}
	return "", false
}

// freshScan runs the injected scanner and returns its output. Take-over must
// resolve the process as it is right now, so the scan is never cached. A scanner
// failure is not fatal: it is logged at debug and treated as an empty table,
// exactly as the Python path swallows an OSError.
func freshScan(ctx context.Context, scan session.ProcessScanner) string {
	ps, err := scan(ctx)
	if err != nil {
		glog.V(4).Infof("[Terminate] Cannot run ps: %v", err)
		return ""
	}
	return ps
}
