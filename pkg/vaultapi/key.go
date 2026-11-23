// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import "path/filepath"

// SnapshotKey identifies one indexed folder the board's snapshot store keys on:
// a vault root plus a vault-relative pages dir. It replaces the page index's
// key so the board no longer imports pkg/pageindex.
type SnapshotKey struct {
	VaultPath string
	PagesDir  string
}

// NewSnapshotKey builds a SnapshotKey, cleaning both parts like the page-index
// key did.
func NewSnapshotKey(vaultPath, pagesDir string) SnapshotKey {
	return SnapshotKey{
		VaultPath: filepath.Clean(vaultPath),
		PagesDir:  filepath.Clean(pagesDir),
	}
}
