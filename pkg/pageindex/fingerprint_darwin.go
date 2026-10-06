// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin

package pageindex

import (
	"os"
	"syscall"
	"time"
)

// statFingerprint returns path's size, modification time and status-change
// time in UTC. It follows symlinks (os.Stat, not os.Lstat).
func statFingerprint(path string) (FileFingerprint, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileFingerprint{}, err
	}
	fingerprint := FileFingerprint{
		Size:    info.Size(),
		ModTime: info.ModTime().UTC(),
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		fingerprint.StatusChangeTime = time.Unix(stat.Ctimespec.Unix()).UTC()
	}
	return fingerprint, nil
}
