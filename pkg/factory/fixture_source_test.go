// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/bborbe/vault-ui/pkg/vaultapi"
)

// sourceFromVault builds a primed vault-api source from the on-disk fixture the
// board used to read through its page index. The board now reads rows from the
// source, so a test that writes fixture pages must hand the equivalent items to
// the handler. It parses only the frontmatter keys the fixtures use.
func sourceFromVault(vaultDir, vaultName, tasksDir, goalsDir, topicsDir string) *vaultapi.Source {
	return vaultapi.NewSourceFromData(
		readTasks(filepath.Join(vaultDir, tasksDir), vaultName),
		readGoals(filepath.Join(vaultDir, goalsDir), vaultName),
		readTopics(filepath.Join(vaultDir, topicsDir), vaultName),
	)
}

func readTasks(dir, vaultName string) []vaultapi.Task {
	var out []vaultapi.Task
	for _, name := range markdownNames(dir) {
		fm := parseFixtureFrontmatter(filepath.Join(dir, name))
		task := vaultapi.Task{
			Title:    strings.TrimSuffix(name, ".md"),
			Vault:    vaultName,
			Status:   fm["status"],
			Phase:    fm["phase"],
			Assignee: fm["assignee"],
		}
		if sid := fm["claude_session_id"]; sid != "" {
			task.ClaudeSessionID = &sid
		}
		out = append(out, task)
	}
	return out
}

func readGoals(dir, vaultName string) []vaultapi.Goal {
	var out []vaultapi.Goal
	for _, name := range markdownNames(dir) {
		fm := parseFixtureFrontmatter(filepath.Join(dir, name))
		out = append(out, vaultapi.Goal{
			Title:  strings.TrimSuffix(name, ".md"),
			Vault:  vaultName,
			Status: fm["status"],
		})
	}
	return out
}

func readTopics(dir, vaultName string) []vaultapi.Topic {
	var out []vaultapi.Topic
	for _, name := range markdownNames(dir) {
		fm := parseFixtureFrontmatter(filepath.Join(dir, name))
		out = append(out, vaultapi.Topic{
			Title:  strings.TrimSuffix(name, ".md"),
			Vault:  vaultName,
			Status: fm["status"],
		})
	}
	return out
}

// markdownNames returns the .md filenames in dir, or nil when dir is absent.
func markdownNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	return names
}

// parseFixtureFrontmatter reads the simple `key: value` lines between the first
// pair of `---` fences. A missing file yields an empty map.
func parseFixtureFrontmatter(path string) map[string]string {
	values := map[string]string{}
	body, err := os.ReadFile(path)
	if err != nil {
		return values
	}
	lines := strings.Split(string(body), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return values
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		kv := strings.SplitN(line, ":", 2)
		if len(kv) == 2 {
			values[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return values
}
