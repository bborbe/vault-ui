// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pageindex is vault-ui's thin adapter over vault-cli's pkg/index page
// index. The engine — immutable per-folder snapshots, stat-diff refresh,
// per-file write marks, change log and the bolt store — lives in vault-cli;
// this package wires vault-ui's glog warnings, prometheus metrics and fixed
// store path into it.
//
// The index holds page metadata only: listed pages carry empty Content, each
// entry carries its pre-parsed Open Questions, and ReadPage reads the full
// page from disk without caching it.
//
// See docs/page-index.md for the staleness bounds, frame ordering and key
// derivation rules.
package pageindex

import (
	"context"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/index"
	"github.com/bborbe/vault-cli/pkg/storage"
	"github.com/golang/glog"
)

// storeApp names vault-ui's directory under the user cache directory.
const storeApp = "vault-ui"

// RescanInterval is the time between stat-diff rescans of every known key.
const RescanInterval = index.RescanInterval

// StoreFormatVersion is the on-disk store layout version. Version 2 records
// carry no page body.
const StoreFormatVersion = index.StoreFormatVersion

// Key identifies one indexed folder: a vault root plus a vault-relative pages dir.
type Key = index.Key

//counterfeiter:generate -o ./mocks/pageindex-page-index.go --fake-name PageIndex . PageIndex

// PageIndex serves vault page metadata from immutable in-memory snapshots.
type PageIndex = index.PageIndex

// Entry is one indexed page plus its pre-parsed Open Questions.
type Entry = index.Entry

//counterfeiter:generate -o ./mocks/pageindex-page-reader.go --fake-name PageReader . PageReader

// PageReader reads page files from a folder.
type PageReader = index.PageReader

//counterfeiter:generate -o ./mocks/pageindex-directory-lister.go --fake-name DirectoryLister . DirectoryLister

// DirectoryLister lists one folder's page-file entries with their fingerprints.
type DirectoryLister = index.DirectoryLister

// FileFingerprint is the pre-read stat of one page file.
type FileFingerprint = index.FileFingerprint

// FileEntry is one directory entry with its current fingerprint.
type FileEntry = index.FileEntry

//counterfeiter:generate -o ./mocks/pageindex-store.go --fake-name Store . Store

// Store persists the metadata-only page index on local disk.
type Store = index.Store

// StoredEntry is one indexed file's persisted record. It never holds a body.
type StoredEntry = index.StoredEntry

// WriterIdentity identifies the process that wrote the store.
type WriterIdentity = index.WriterIdentity

// NewKey builds a Key, applying filepath.Clean to both parts.
func NewKey(vaultPath, pagesDir string) Key {
	return index.NewKey(vaultPath, pagesDir)
}

// NewPageReader returns the production reader over vault-cli's page storage.
func NewPageReader(pages storage.PageStorage) PageReader {
	return index.NewPageReader(pages)
}

// NewDirectoryLister returns the production directory lister.
func NewDirectoryLister() DirectoryLister {
	return index.NewDirectoryLister()
}

// NewPageIndex creates an empty page index with no store, reporting to
// vault-ui's prometheus metrics and warning through glog.
func NewPageIndex(
	reader PageReader,
	lister DirectoryLister,
	currentDateTimeGetter libtime.CurrentDateTimeGetter,
	waiter libtime.WaiterDuration,
) PageIndex {
	idx := index.NewPageIndex(reader, lister, currentDateTimeGetter, waiter, NewMetrics())
	index.SetWarnf(idx, glog.Warningf)
	return idx
}

// NewPageIndexWithStore creates an empty page index hydrated from and written
// through to store, reporting to vault-ui's prometheus metrics and warning
// through glog. A nil store disables persistence.
func NewPageIndexWithStore(
	reader PageReader,
	lister DirectoryLister,
	currentDateTimeGetter libtime.CurrentDateTimeGetter,
	waiter libtime.WaiterDuration,
	store Store,
) PageIndex {
	idx := index.NewPageIndexWithStore(
		reader, lister, currentDateTimeGetter, waiter, NewMetrics(), store,
	)
	index.SetWarnf(idx, glog.Warningf)
	return idx
}

// DefaultStorePath returns <user cache directory>/vault-ui/page-index.bolt.
func DefaultStorePath() (string, error) {
	return index.DefaultStorePath(storeApp)
}

// CurrentWriterIdentity returns this process's writer identity.
func CurrentWriterIdentity(ctx context.Context) WriterIdentity {
	return index.CurrentWriterIdentity(ctx)
}

// NewBoltStore opens the embedded-Bolt store at path under the given writer
// identity. It never returns nil and never fails: a store written under another
// format (the pre-upgrade body-holding layout) or parser version is discarded
// with one warning naming the path and the reason.
func NewBoltStore(
	ctx context.Context,
	path string,
	identity WriterIdentity,
	warnf func(format string, args ...any),
	vlogf func(format string, args ...any),
) Store {
	return index.NewBoltStore(ctx, path, identity, warnf, vlogf)
}

// OpenDefaultStore opens the store at DefaultStorePath with the current writer
// identity. It never returns nil and never fails: an unresolvable location
// yields a store that serves nothing.
func OpenDefaultStore(
	ctx context.Context,
	warnf func(format string, args ...any),
	vlogf func(format string, args ...any),
) Store {
	path, err := DefaultStorePath()
	if err != nil {
		warnf("page index store disabled: resolve store path: %v", err)
		return &noStore{}
	}
	return NewBoltStore(ctx, path, CurrentWriterIdentity(ctx), warnf, vlogf)
}

// noStore serves nothing and persists nothing.
type noStore struct{}

func (*noStore) Load(context.Context, Key) ([]StoredEntry, bool, error) { return nil, false, nil }

func (*noStore) Write(context.Context, Key, []StoredEntry, []string) error { return nil }

func (*noStore) Path() string { return "" }

func (*noStore) Close() error { return nil }
