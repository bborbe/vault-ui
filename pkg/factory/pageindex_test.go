// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/bborbe/vault-cli/mocks"
	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/storage"
	. "github.com/onsi/gomega"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/pageindex"
	"github.com/bborbe/vault-ui/pkg/queue"
	"github.com/bborbe/vault-ui/pkg/sessionlock"
	"github.com/bborbe/vault-ui/pkg/statuscache"
	"github.com/bborbe/vault-ui/pkg/vaultapi"
	"github.com/bborbe/vault-ui/pkg/websocket"
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

// countingSeams counts reads and listings while delegating to the real disk
// seams, so responses stay byte-identical. Reads are counted per filename and
// listings per key, so a test can tell a per-file update from a stat-diff.
type countingSeams struct {
	pageindex.PageReader
	pageindex.DirectoryLister

	mu        sync.Mutex
	reads     int
	readCalls []seamRead
	listCalls [][2]string
}

// seamRead is one ReadEntry call.
type seamRead struct {
	key      [2]string
	filename string
}

func newCountingSeams() *countingSeams {
	return &countingSeams{
		PageReader:      pageindex.NewPageReader(storage.NewPageStorage(nil)),
		DirectoryLister: pageindex.NewDirectoryLister(),
	}
}

func (c *countingSeams) ListFiles(
	ctx context.Context,
	vaultPath string,
	pagesDir string,
) ([]pageindex.FileEntry, error) {
	c.mu.Lock()
	c.listCalls = append(c.listCalls, [2]string{vaultPath, pagesDir})
	c.mu.Unlock()
	return c.DirectoryLister.ListFiles(ctx, vaultPath, pagesDir)
}

func (c *countingSeams) ReadEntry(
	ctx context.Context,
	vaultPath string,
	pagesDir string,
	filename string,
) (*pageindex.Entry, pageindex.FileFingerprint, error) {
	c.mu.Lock()
	c.reads++
	c.readCalls = append(c.readCalls, seamRead{
		key:      [2]string{vaultPath, pagesDir},
		filename: filename,
	})
	c.mu.Unlock()
	return c.PageReader.ReadEntry(ctx, vaultPath, pagesDir, filename)
}

// reset clears the recorded reads and listings, so a fixture can count only
// what the test's own requests caused.
func (c *countingSeams) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads = 0
	c.readCalls = nil
	c.listCalls = nil
}

// readCounts returns, per key and filename, the number of single-file reads.
func (c *countingSeams) readCounts() map[[2]string]map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	counts := map[[2]string]map[string]int{}
	for _, call := range c.readCalls {
		if counts[call.key] == nil {
			counts[call.key] = map[string]int{}
		}
		counts[call.key][call.filename]++
	}
	return counts
}

// readsFor returns how many times one file of one key was read.
func (c *countingSeams) readsFor(key [2]string, filename string) int {
	return c.readCounts()[key][filename]
}

// readsForKey returns every filename read for one key, with its count.
func (c *countingSeams) readsForKey(key [2]string) map[string]int {
	return c.readCounts()[key]
}

// listCounts returns the per-(vaultPath, pagesDir) listing count.
func (c *countingSeams) listCounts() map[[2]string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	counts := map[[2]string]int{}
	for _, call := range c.listCalls {
		counts[call]++
	}
	return counts
}

// readCount returns the total single-file reads the index has made.
func (c *countingSeams) readCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

func (c *countingSeams) totalListCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.listCalls)
}

// indexHandler wires CreateAPIHandler over the given index and write queue. The
// board's read source is an empty vault-api source: these mutation fixtures
// assert page-index invalidation, not board rows.
func indexHandler(
	loader config.Loader,
	configPath string,
	pageIndex pageindex.PageIndex,
	writeQueue queue.Queue,
) http.Handler {
	readiness := vaultui.NewReadiness()
	readiness.SetReady()
	return factory.CreateAPIHandler(
		loader, configPath, statuscache.NewCache(), factory.CreatePaneResolver(tempDir()),
		launchregistry.NewRegistry(), sessionlock.NewRegistry(), tempDir(), readiness,
		websocket.NewConnectionManager(websocket.NewMetrics()), pageIndex,
		factory.CreateSessionSnapshot(factory.CreateSessionState()),
		writeQueue,
		vaultapi.NewSourceFromData(nil, nil, nil),
	)
}
