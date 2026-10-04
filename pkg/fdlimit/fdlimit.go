// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fdlimit raises the process's own file-descriptor limit at startup.
//
// The vault watcher runs in-process and holds roughly one descriptor per
// watched file across every configured vault — about 10,000 on a twelve-vault
// machine. The process inherits whatever limit its launcher granted, and
// launchd grants a small default, so the watcher exhausts it and the server
// stops accepting connections. Raising the limit here makes the service work
// regardless of how it was launched.
package fdlimit

import (
	"context"
	"syscall"

	"github.com/bborbe/errors"
)

// MaxLimit is the descriptor limit requested when the hard limit is infinite.
// It sits comfortably above the ~10,000 descriptors a twelve-vault watcher
// needs while staying well below the ranges that make some kernels refuse the
// request.
const MaxLimit uint64 = 65536

// UnknownLimit is returned by Raise when the current limit could not be read,
// so a caller can tell "nothing was applied" apart from a real limit. No
// process runs with a file-descriptor limit of zero.
const UnknownLimit uint64 = 0

// TargetLimit returns the descriptor limit to request given the process's
// current soft limit and the hard limit. It never lowers the limit and never
// requests more than the hard limit, except that a hard limit above MaxLimit is
// capped at MaxLimit. That cap is what handles an infinite hard limit: linux
// reports it as -1 (0xffffffffffffffff as a uint64) and darwin as
// 0x7fffffffffffffff, both far above MaxLimit, so neither needs detecting
// separately — and detecting them by comparison would not compile on linux,
// where RLIM_INFINITY is an untyped -1 that overflows a uint64.
func TargetLimit(current, hard uint64) uint64 {
	target := min(hard, MaxLimit)
	if target < current {
		return current
	}
	return target
}

// Raise reads the current file-descriptor limits, computes the target with
// TargetLimit, and applies it. It returns the resulting soft limit.
//
// On a Setrlimit failure it returns the soft limit still in effect, so the
// caller can report what was actually applied. When the current limit cannot be
// read at all it returns UnknownLimit: nothing was applied and the real value
// is not known, so a caller must not report it as an applied limit.
func Raise(ctx context.Context) (uint64, error) {
	var before syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &before); err != nil {
		return UnknownLimit, errors.Wrap(ctx, err, "get file descriptor limit")
	}

	target := TargetLimit(before.Cur, before.Max)
	if target == before.Cur {
		return before.Cur, nil
	}

	after := before
	after.Cur = target
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &after); err != nil {
		return before.Cur, errors.Wrap(ctx, err, "set file descriptor limit")
	}
	return target, nil
}
