// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bborbe/errors"
)

// directoryLister lists one folder's page files. It mirrors vault-cli's folder
// walk so the entry set, order and exclusions match its own listing.
type directoryLister struct{}

// ListFiles returns the folder's page-file entries in os.ReadDir order.
func (l *directoryLister) ListFiles(
	ctx context.Context,
	vaultPath string,
	pagesDir string,
) ([]FileEntry, error) {
	targetDir := filepath.Join(vaultPath, pagesDir)
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, errors.Wrapf(ctx, err, "read directory %s", targetDir)
	}

	result := make([]FileEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		fingerprint, statErr := statFingerprint(filepath.Join(targetDir, entry.Name()))
		if statErr != nil {
			// A zero fingerprint keeps the entry so the reader's exclusion path
			// runs, and stops a later stat-diff from re-reading it every pass.
			fingerprint = FileFingerprint{}
		}
		result = append(result, FileEntry{Name: entry.Name(), Fingerprint: fingerprint})
	}
	return result, nil
}
