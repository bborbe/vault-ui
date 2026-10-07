// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/bborbe/errors"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/storage"
)

// pageReader reads one page file from disk through vault-cli's single-page
// read, so a page parsed here is the page vault-cli's own folder listing
// produces.
type pageReader struct {
	pages storage.PageStorage
}

// ReadPage parses one page file and reports its pre-read fingerprint.
//
// The fingerprint is this package's own — it is taken before the read so the
// stat-diff can decide whether a later read is needed — while the read itself
// delegates to vault-cli's storage.PageStorage.ReadPage, which enforces the
// symlink-out-of-vault guard and the frontmatter parse.
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

	page, err := r.pages.ReadPage(
		ctx,
		vaultPath,
		pagesDir,
		strings.TrimSuffix(filename, ".md"),
	)
	if err != nil {
		return nil, fingerprint, err
	}
	return page, fingerprint, nil
}
