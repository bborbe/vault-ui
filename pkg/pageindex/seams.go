// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex

import (
	"context"
	"time"

	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/storage"
)

// FileFingerprint is the pre-read stat of one page file. Size, ModTime and
// StatusChangeTime come from a single stat that follows symlinks.
type FileFingerprint struct {
	Size             int64
	ModTime          time.Time
	StatusChangeTime time.Time
}

// FileEntry is one directory entry with its current fingerprint.
type FileEntry struct {
	Name        string
	Fingerprint FileFingerprint
}

//counterfeiter:generate -o ./mocks/pageindex-page-reader.go --fake-name PageReader . PageReader

// PageReader reads one page file from a folder. The filename is the file's
// base name including the ".md" suffix. It returns the parsed page and the
// file's pre-read fingerprint; a non-nil error means the file is excluded
// (absent, unreadable, unparsable, or a symlink out of the vault) and the
// returned fingerprint is still the best available (zero when the stat failed).
type PageReader interface {
	ReadPage(
		ctx context.Context,
		vaultPath string,
		pagesDir string,
		filename string,
	) (*domain.Page, FileFingerprint, error)
}

//counterfeiter:generate -o ./mocks/pageindex-directory-lister.go --fake-name DirectoryLister . DirectoryLister

// DirectoryLister lists one folder's page-file entries with their current
// fingerprints, in os.ReadDir order (filename ascending).
type DirectoryLister interface {
	ListFiles(ctx context.Context, vaultPath string, pagesDir string) ([]FileEntry, error)
}

// NewPageReader returns the production single-file reader.
func NewPageReader(pages storage.PageStorage) PageReader {
	return &pageReader{pages: pages}
}

// NewDirectoryLister returns the production directory lister.
func NewDirectoryLister() DirectoryLister {
	return &directoryLister{}
}
