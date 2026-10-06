// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/mocks"
	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/domain"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/pageindex"
	"github.com/bborbe/vault-ui/pkg/queue"
	"github.com/bborbe/vault-ui/pkg/statuscache"
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
// seams, so responses stay byte-identical.
type countingSeams struct {
	pageindex.PageReader
	pageindex.DirectoryLister

	mu        sync.Mutex
	reads     int
	listCalls [][2]string
}

func newCountingSeams() *countingSeams {
	return &countingSeams{
		PageReader:      pageindex.NewPageReader(),
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

func (c *countingSeams) ReadPage(
	ctx context.Context,
	vaultPath string,
	pagesDir string,
	filename string,
) (*domain.Page, pageindex.FileFingerprint, error) {
	c.mu.Lock()
	c.reads++
	c.mu.Unlock()
	return c.PageReader.ReadPage(ctx, vaultPath, pagesDir, filename)
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

// indexHandler wires CreateAPIHandler exactly as production does, over the
// given index and write queue.
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
		launchregistry.NewRegistry(), tempDir(), readiness,
		websocket.NewConnectionManager(websocket.NewMetrics()), pageIndex,
		factory.CreateSessionState(),
		writeQueue,
	)
}

var _ = Describe("Page index read wiring", func() {
	It("serves warm list reads without touching page storage", func() {
		alpha := newIndexVault("alpha")
		beta := newIndexVault("beta")
		loader, configPath := indexLoader(alpha, beta)
		seams := newCountingSeams()
		pageIndex := factory.CreatePageIndex(seams, seams, libtime.NewCurrentDateTime())
		handler := indexHandler(loader, configPath, pageIndex, startWriteQueue())

		Expect(
			factory.CreatePageIndexWarmup(loader, configPath, pageIndex)(context.Background()),
		).To(Succeed())

		// Positive control: the warm-up built every (vault, folder) pair.
		counts := seams.listCounts()
		for _, pair := range [][2]string{
			{alpha.path, alpha.tasks}, {alpha.path, alpha.goals}, {alpha.path, alpha.topics},
			{beta.path, beta.tasks}, {beta.path, beta.goals}, {beta.path, beta.topics},
		} {
			Expect(counts[pair]).To(BeNumerically(">=", 1), "warm-up must build %v", pair)
		}

		warmCalls := seams.totalListCalls()

		targets := []struct {
			target string
			body   string
		}{
			{"/api/tasks", "Task A"},
			{"/api/assignees", "alice"},
			{"/api/goals", "Goal A"},
			{"/api/topics", "TopicOne"},
			{"/api/topics/TopicOne?vault=alpha", "TopicOne"},
		}
		for _, target := range targets {
			for i := 0; i < 5; i++ {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(
					recorder,
					httptest.NewRequest(http.MethodGet, target.target, nil),
				)
				Expect(recorder.Code).To(Equal(http.StatusOK), "%s", target.target)
				Expect(recorder.Body.String()).To(ContainSubstring(target.body), "%s", target.target)
			}
		}

		// Evidence: 25 warm requests caused exactly 0 additional reads.
		Expect(seams.totalListCalls()).To(Equal(warmCalls))
	})

	Describe("CreatePageIndexWarmup", func() {
		It("fails when the config cannot be loaded", func() {
			loader, _ := indexLoader(newIndexVault("alpha"))
			seams := newCountingSeams()
			pageIndex := factory.CreatePageIndex(seams, seams, libtime.NewCurrentDateTime())

			err := factory.CreatePageIndexWarmup(
				loader, filepath.Join(tempDir(), "missing.yaml"), pageIndex,
			)(context.Background())
			Expect(err).To(HaveOccurred())
		})

		It("returns nil when the context is cancelled", func() {
			loader, configPath := indexLoader(newIndexVault("alpha"))
			seams := newCountingSeams()
			pageIndex := factory.CreatePageIndex(seams, seams, libtime.NewCurrentDateTime())

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			Expect(
				factory.CreatePageIndexWarmup(loader, configPath, pageIndex)(ctx),
			).To(Succeed())
		})

		It("skips the topics key for a vault without a topics folder", func() {
			dir := tempDir()
			Expect(os.MkdirAll(filepath.Join(dir, "Tasks"), 0750)).To(Succeed())
			Expect(os.MkdirAll(filepath.Join(dir, "Goals"), 0750)).To(Succeed())
			writeFile(dir, "Tasks/Task A.md", "---\nstatus: next\n---\n# Task A\n")
			writeFile(dir, "Goals/Goal A.md", "---\nstatus: in_progress\n---\n# Goal A\n")

			loader := &mocks.Loader{}
			loader.GetAllVaultsReturns([]*config.Vault{{
				Name: "alpha", Path: dir, TasksDir: "Tasks", GoalsDir: "Goals",
			}}, nil)
			loader.GetCurrentUserReturns("tester", nil)
			configPath := filepath.Join(tempDir(), "config.yaml")
			Expect(os.WriteFile(configPath, []byte("host: 127.0.0.1\n"), 0600)).To(Succeed())

			seams := newCountingSeams()
			pageIndex := factory.CreatePageIndex(seams, seams, libtime.NewCurrentDateTime())
			Expect(
				factory.CreatePageIndexWarmup(loader, configPath, pageIndex)(context.Background()),
			).To(Succeed())

			counts := seams.listCounts()
			Expect(counts[[2]string{dir, "Tasks"}]).To(BeNumerically(">=", 1))
			Expect(counts[[2]string{dir, "Goals"}]).To(BeNumerically(">=", 1))
			Expect(counts[[2]string{dir, ""}]).To(Equal(0))
		})
	})
})
