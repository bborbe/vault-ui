// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bborbe/errors"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/storage"
)

// pageReader reads one page file from disk. It mirrors vault-cli's own
// single-file read so a page parsed here is identical to a page parsed by
// vault-cli's folder listing.
type pageReader struct{}

// ReadPage parses one page file and reports its pre-read fingerprint.
func (r *pageReader) ReadPage(
	ctx context.Context,
	vaultPath string,
	pagesDir string,
	filename string,
) (*domain.Page, FileFingerprint, error) {
	filePath := filepath.Join(vaultPath, pagesDir, filename)

	fingerprint, err := statFingerprint(filePath)
	if err != nil {
		return nil, FileFingerprint{}, errors.Wrapf(ctx, err, "stat %s", filePath)
	}
	if isSymlinkOutsideVault(filePath, vaultPath) {
		return nil, fingerprint, errors.Errorf(ctx, "symlink outside vault: %s", filePath)
	}

	content, err := os.ReadFile(filePath) //#nosec G304 -- user-controlled vault path
	if err != nil {
		return nil, fingerprint, errors.Wrapf(ctx, err, "read file %s", filePath)
	}

	// A failed second stat leaves ModifiedDate nil, exactly as vault-cli does.
	var modTime *time.Time
	if info, statErr := os.Stat(filePath); statErr == nil {
		t := info.ModTime().UTC()
		modTime = &t
	}

	data, parseErr := storage.ParseFrontmatterMap(ctx, content)
	if parseErr != nil {
		return nil, fingerprint, errors.Wrapf(ctx, parseErr, "parse frontmatter %s", filePath)
	}

	page := domain.NewPage(
		data,
		domain.FileMetadata{
			Name:         strings.TrimSuffix(filename, ".md"),
			FilePath:     filePath,
			ModifiedDate: modTime,
		},
		domain.Content(content),
	)
	return page, fingerprint, nil
}

// isSymlinkOutsideVault returns true when path is a symlink resolving outside
// vaultPath. A broken symlink counts as outside. Non-symlinks return false.
func isSymlinkOutsideVault(path, vaultPath string) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return false
	}

	resolvedVault, err := filepath.EvalSymlinks(vaultPath)
	if err != nil {
		return false
	}
	absVault, err := filepath.Abs(resolvedVault)
	if err != nil {
		return false
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		// A broken symlink is treated as unsafe.
		return true
	}
	absResolved, err := filepath.Abs(resolved)
	if err != nil {
		return true
	}
	return !strings.HasPrefix(absResolved, absVault)
}
