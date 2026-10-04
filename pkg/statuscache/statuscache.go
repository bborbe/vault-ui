// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package statuscache reproduces the Python vault_ui.status_cache in-memory
// cache: it loads each discovered hierarchy folder's items, extracting the
// frontmatter `status` and the raw `claude_session_started` marker, and
// invalidates a single item on a watcher event.
//
// The cache is the sanctioned direct-read path for the `claude_session_started`
// marker: vault-cli's task list does not emit that custom field. It reads vault
// files only to extract frontmatter and never writes them.
package statuscache

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/bborbe/vault-ui/pkg/hierarchy"
)

// defaultTasksFolder is the preferred tasks folder used when a vault has none
// configured. It mirrors the Python "24 Tasks" fallback.
const defaultTasksFolder = "24 Tasks"

// frontmatterPattern matches the leading YAML frontmatter block. The (?s) flag
// makes `.` match newlines, mirroring Python's re.DOTALL, and the non-greedy
// group stops at the first closing fence.
var frontmatterPattern = regexp.MustCompile(`(?s)^---\s*\n(.*?)\n---`)

// Cache is the in-memory status + claude_session_started cache.
type Cache interface {
	// LoadVault scans every discovered hierarchy folder for *.md files and
	// replaces the vault's cached statuses and markers.
	LoadVault(vaultName, vaultPath, tasksFolder string) error
	// GetStatus returns the item's cached status, and whether it is present.
	GetStatus(vaultName, itemID string) (string, bool)
	// GetSessionStarted returns the item's raw claude_session_started marker, and
	// whether it is present.
	GetSessionStarted(vaultName, itemID string) (string, bool)
	// Count returns the number of cached statuses for a vault.
	Count(vaultName string) int
	// Invalidate re-reads a single item from disk, updating status and marker
	// together, or drops the item when its file is gone.
	Invalidate(vaultName, itemID string)
}

type cache struct {
	mu           sync.RWMutex
	statuses     map[string]map[string]string
	started      map[string]map[string]string
	vaultPaths   map[string]string
	tasksFolders map[string]string
}

// NewCache creates an empty status cache.
func NewCache() Cache {
	return &cache{
		statuses:     map[string]map[string]string{},
		started:      map[string]map[string]string{},
		vaultPaths:   map[string]string{},
		tasksFolders: map[string]string{},
	}
}

func (c *cache) LoadVault(vaultName, vaultPath, tasksFolder string) error {
	tasksFolder = orDefault(tasksFolder)

	statuses := map[string]string{}
	started := map[string]string{}

	folders, err := hierarchy.DiscoverHierarchyFoldersForVault(vaultPath, tasksFolder)
	if err != nil {
		return err
	}
	for _, folder := range folders {
		_ = filepath.WalkDir(folder, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				return nil
			}
			itemID := strings.TrimSuffix(entry.Name(), ".md")
			status, sessionStarted := extractFields(path)
			if status != "" {
				statuses[itemID] = status
			}
			if sessionStarted != "" {
				started[itemID] = sessionStarted
			}
			return nil
		})
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.statuses[vaultName] = statuses
	c.started[vaultName] = started
	c.vaultPaths[vaultName] = vaultPath
	c.tasksFolders[vaultName] = tasksFolder
	return nil
}

func (c *cache) GetStatus(vaultName, itemID string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	status, ok := c.statuses[vaultName][itemID]
	return status, ok
}

func (c *cache) GetSessionStarted(vaultName, itemID string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	started, ok := c.started[vaultName][itemID]
	return started, ok
}

func (c *cache) Count(vaultName string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.statuses[vaultName])
}

func (c *cache) Invalidate(vaultName, itemID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	vaultPath, ok := c.vaultPaths[vaultName]
	if !ok {
		return
	}

	folders, err := hierarchy.DiscoverHierarchyFoldersForVault(
		vaultPath,
		orDefault(c.tasksFolders[vaultName]),
	)
	if err != nil {
		return
	}

	for _, folder := range folders {
		mdFile := filepath.Join(folder, itemID+".md")
		if !isFile(mdFile) {
			continue
		}
		status, sessionStarted := extractFields(mdFile)
		if status != "" {
			if c.statuses[vaultName] == nil {
				c.statuses[vaultName] = map[string]string{}
			}
			c.statuses[vaultName][itemID] = status
		} else {
			delete(c.statuses[vaultName], itemID)
		}
		if sessionStarted != "" {
			if c.started[vaultName] == nil {
				c.started[vaultName] = map[string]string{}
			}
			c.started[vaultName][itemID] = sessionStarted
		} else {
			delete(c.started[vaultName], itemID)
		}
		return
	}

	// File deleted or moved - drop the item from both maps.
	delete(c.statuses[vaultName], itemID)
	delete(c.started[vaultName], itemID)
}

// extractFields reads a file's frontmatter and returns (status, sessionStarted).
// Both are "" when absent or on any read/parse failure, so a malformed file
// contributes nothing and never crashes the load.
func extractFields(filePath string) (string, string) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", ""
	}

	match := frontmatterPattern.FindStringSubmatch(string(content))
	if match == nil {
		return "", ""
	}

	var frontmatter map[string]any
	if err := yaml.Unmarshal([]byte(match[1]), &frontmatter); err != nil {
		return "", ""
	}

	return truthyString(frontmatter["status"]), startedString(frontmatter["claude_session_started"])
}

// truthyString returns the string form of a truthy value, or "" for a falsy one.
func truthyString(value any) string {
	if !truthy(value) {
		return ""
	}
	return toString(value)
}

// startedString normalises a truthy claude_session_started value. A legacy YAML
// boolean true becomes the literal "true"; any other value (an ISO-8601 launch
// timestamp since 2026-08-29) is preserved verbatim.
func startedString(value any) string {
	if !truthy(value) {
		return ""
	}
	if boolean, ok := value.(bool); ok && boolean {
		return "true"
	}
	return toString(value)
}

// truthy mirrors Python truthiness for the scalar values YAML can produce.
func truthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	default:
		return true
	}
}

// toString renders a scalar value as a string.
func toString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case time.Time:
		return typed.Format(time.RFC3339Nano)
	default:
		return fmt.Sprintf("%v", value)
	}
}

// orDefault returns tasksFolder, or the default when empty.
func orDefault(tasksFolder string) string {
	if tasksFolder == "" {
		return defaultTasksFolder
	}
	return tasksFolder
}

// isFile reports whether path is an existing regular file.
func isFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
