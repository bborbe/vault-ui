// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"os"
	"path/filepath"

	"github.com/bborbe/vault-cli/mocks"
	"github.com/bborbe/vault-cli/pkg/config"
	. "github.com/onsi/gomega"
)

const (
	indexTasksDir  = "24 Tasks"
	indexGoalsDir  = "23 Goals"
	indexTopicsDir = "25 Topics"
)

// indexVault is one on-disk vault fixture: a root plus its three read folders.
type indexVault struct {
	name   string
	path   string
	tasks  string
	goals  string
	topics string
}

// newIndexVault creates a temp vault with one task, one goal and one topic file.
func newIndexVault(name string) indexVault {
	dir := tempDir()
	for _, folder := range []string{indexTasksDir, indexGoalsDir, indexTopicsDir} {
		Expect(os.MkdirAll(filepath.Join(dir, folder), 0750)).To(Succeed())
	}
	writeFile(
		dir, indexTasksDir+"/Task A.md",
		"---\nstatus: next\nassignee: alice\n---\n# Task A\n",
	)
	writeFile(dir, indexGoalsDir+"/Goal A.md", "---\nstatus: in_progress\n---\n# Goal A\n")
	writeFile(
		dir, indexTopicsDir+"/TopicOne.md",
		"---\npage_type: topic\n---\n# TopicOne\n\n## Goals\n\n- [[Goal A]]\n",
	)
	return indexVault{
		name:   name,
		path:   dir,
		tasks:  indexTasksDir,
		goals:  indexGoalsDir,
		topics: indexTopicsDir,
	}
}

// indexLoader builds a loader + config.yaml naming the given vaults.
func indexLoader(vaults ...indexVault) (*mocks.Loader, string) {
	cliVaults := make([]*config.Vault, 0, len(vaults))
	for _, vault := range vaults {
		cliVaults = append(cliVaults, &config.Vault{
			Name:      vault.name,
			Path:      vault.path,
			TasksDir:  vault.tasks,
			GoalsDir:  vault.goals,
			TopicsDir: vault.topics,
		})
	}
	loader := &mocks.Loader{}
	loader.GetAllVaultsReturns(cliVaults, nil)
	loader.GetCurrentUserReturns("tester", nil)

	configPath := filepath.Join(tempDir(), "config.yaml")
	Expect(os.WriteFile(configPath, []byte("host: 127.0.0.1\n"), 0600)).To(Succeed())
	return loader, configPath
}
