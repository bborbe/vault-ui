// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package hierarchy reproduces the Python vault_ui.hierarchy folder-discovery
// behavior: it finds a vault's Themes/Objectives/Goals/Tasks folders, orders
// them by category then numeric prefix, and prefers a vault's configured tasks
// folder while excluding the other *Tasks folders in the same vault.
package hierarchy

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Suffixes are the recognized hierarchy folder suffixes, in category order.
var Suffixes = []string{"Themes", "Objectives", "Goals", "Tasks"}

// missingPrefix is the sort weight used for a folder whose name carries no
// numeric prefix, so such folders order last within their category. It mirrors
// the Python 9999 sentinel.
const missingPrefix = 9999

// DiscoverHierarchyFolders returns the top-level directories under vaultPath whose
// name ends with one of Suffixes, ordered by category (Suffixes order) then by
// numeric prefix, then case-insensitively by name. A missing vault path returns an
// empty slice and no error.
//
// An unreadable vault path also yields an empty slice rather than an error: the
// Python contract gates on Path.exists(), which swallows the OSError from a
// failed stat and reports the path as absent.
func DiscoverHierarchyFolders(vaultPath string) ([]string, error) {
	entries, err := os.ReadDir(vaultPath)
	if err != nil {
		return nil, nil
	}

	folders := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !isDir(filepath.Join(vaultPath, entry.Name())) {
			continue
		}
		if suffixFor(entry.Name()) == "" {
			continue
		}
		folders = append(folders, filepath.Join(vaultPath, entry.Name()))
	}

	sort.SliceStable(folders, func(i, j int) bool {
		left := sortKey(filepath.Base(folders[i]))
		right := sortKey(filepath.Base(folders[j]))
		if left.category != right.category {
			return left.category < right.category
		}
		if left.prefix != right.prefix {
			return left.prefix < right.prefix
		}
		return left.name < right.name
	})

	return folders, nil
}

// DiscoverHierarchyFoldersForVault keeps all Themes/Objectives/Goals folders and
// prefers exactly the configured tasksFolder among the *Tasks folders (excluding
// the others). If the configured folder is absent from the discovered set, the
// discovered set is returned unchanged. Returns full paths.
func DiscoverHierarchyFoldersForVault(vaultPath, tasksFolder string) ([]string, error) {
	folders, err := DiscoverHierarchyFolders(vaultPath)
	if err != nil {
		return nil, err
	}

	nonTasks := make([]string, 0, len(folders))
	taskFolders := make([]string, 0, len(folders))
	for _, folder := range folders {
		if strings.HasSuffix(filepath.Base(folder), "Tasks") {
			taskFolders = append(taskFolders, folder)
			continue
		}
		nonTasks = append(nonTasks, folder)
	}

	preferred := filepath.Join(vaultPath, tasksFolder)
	for _, taskFolder := range taskFolders {
		if taskFolder == preferred {
			return append(nonTasks, preferred), nil
		}
	}

	return folders, nil
}

// isDir reports whether path is a directory, following symlinks to match the
// Python Path.is_dir() contract.
func isDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// key is a folder's computed sort tuple.
type key struct {
	category int
	prefix   int
	name     string
}

// sortKey computes the Python sort tuple for a folder name: the category index,
// the numeric prefix, and the lower-cased name.
func sortKey(name string) key {
	suffix := suffixFor(name)
	prefix := missingPrefix
	if suffix != "" {
		raw := strings.TrimSpace(strings.TrimSuffix(name, suffix))
		if raw != "" {
			fields := strings.Fields(raw)
			if len(fields) > 0 {
				if parsed, err := strconv.Atoi(fields[0]); err == nil {
					prefix = parsed
				}
			}
		}
	}
	return key{
		category: categoryFor(suffix),
		prefix:   prefix,
		name:     strings.ToLower(name),
	}
}

// suffixFor returns the first Suffixes entry the name ends with, or "" when none
// matches.
func suffixFor(name string) string {
	for _, suffix := range Suffixes {
		if strings.HasSuffix(name, suffix) {
			return suffix
		}
	}
	return ""
}

// categoryFor returns the Suffixes position of a suffix, or the missing weight
// when the suffix is unknown.
func categoryFor(suffix string) int {
	for idx, candidate := range Suffixes {
		if candidate == suffix {
			return idx
		}
	}
	return missingPrefix
}
