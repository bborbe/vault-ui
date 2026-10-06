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

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/mocks"
	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/pageindex"
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

// countingPageStorage is a PageStorage fake that counts calls while delegating
// to the real disk reader, so responses stay byte-identical.
func countingPageStorage() *mocks.PageStorage {
	real := storage.NewPageStorage(nil)
	fake := &mocks.PageStorage{}
	fake.ListPagesStub = func(
		ctx context.Context,
		vaultPath string,
		pagesDir string,
	) ([]*domain.Page, error) {
		return real.ListPages(ctx, vaultPath, pagesDir)
	}
	return fake
}

// listPagesCounts returns the per-(vaultPath, pagesDir) call count.
func listPagesCounts(fake *mocks.PageStorage) map[[2]string]int {
	counts := map[[2]string]int{}
	for i := 0; i < fake.ListPagesCallCount(); i++ {
		_, vaultPath, pagesDir := fake.ListPagesArgsForCall(i)
		counts[[2]string{vaultPath, pagesDir}]++
	}
	return counts
}

// indexHandler wires CreateAPIHandler exactly as production does, over the
// given index.
func indexHandler(
	loader config.Loader,
	configPath string,
	pageIndex pageindex.PageIndex,
) http.Handler {
	readiness := vaultui.NewReadiness()
	readiness.SetReady()
	return factory.CreateAPIHandler(
		loader, configPath, statuscache.NewCache(), factory.CreatePaneResolver(tempDir()),
		launchregistry.NewRegistry(), tempDir(), readiness,
		websocket.NewConnectionManager(websocket.NewMetrics()), pageIndex,
	)
}

var _ = Describe("Page index read wiring", func() {
	It("serves warm list reads without touching page storage", func() {
		alpha := newIndexVault("alpha")
		beta := newIndexVault("beta")
		loader, configPath := indexLoader(alpha, beta)
		fake := countingPageStorage()
		pageIndex := factory.CreatePageIndex(fake, libtime.NewCurrentDateTime())
		handler := indexHandler(loader, configPath, pageIndex)

		Expect(
			factory.CreatePageIndexWarmup(loader, configPath, pageIndex)(context.Background()),
		).To(Succeed())

		// Positive control: the warm-up built every (vault, folder) pair.
		counts := listPagesCounts(fake)
		for _, pair := range [][2]string{
			{alpha.path, alpha.tasks}, {alpha.path, alpha.goals}, {alpha.path, alpha.topics},
			{beta.path, beta.tasks}, {beta.path, beta.goals}, {beta.path, beta.topics},
		} {
			Expect(counts[pair]).To(BeNumerically(">=", 1), "warm-up must build %v", pair)
		}

		warmCalls := fake.ListPagesCallCount()

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
		Expect(fake.ListPagesCallCount()).To(Equal(warmCalls))
	})

	Describe("CreatePageIndexWarmup", func() {
		It("fails when the config cannot be loaded", func() {
			loader, _ := indexLoader(newIndexVault("alpha"))
			pageIndex := factory.CreatePageIndex(countingPageStorage(), libtime.NewCurrentDateTime())

			err := factory.CreatePageIndexWarmup(
				loader, filepath.Join(tempDir(), "missing.yaml"), pageIndex,
			)(context.Background())
			Expect(err).To(HaveOccurred())
		})

		It("returns nil when the context is cancelled", func() {
			loader, configPath := indexLoader(newIndexVault("alpha"))
			pageIndex := factory.CreatePageIndex(countingPageStorage(), libtime.NewCurrentDateTime())

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

			fake := countingPageStorage()
			pageIndex := factory.CreatePageIndex(fake, libtime.NewCurrentDateTime())
			Expect(
				factory.CreatePageIndexWarmup(loader, configPath, pageIndex)(context.Background()),
			).To(Succeed())

			counts := listPagesCounts(fake)
			Expect(counts[[2]string{dir, "Tasks"}]).To(BeNumerically(">=", 1))
			Expect(counts[[2]string{dir, "Goals"}]).To(BeNumerically(">=", 1))
			Expect(counts[[2]string{dir, ""}]).To(Equal(0))
		})
	})
})
