// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/sessionlock"
	"github.com/bborbe/vault-ui/pkg/statuscache"
)

// seedTaskMarker rewrites the vault's task file so it carries
// claude_session_started, and loads the vault into the status cache.
func seedTaskMarker(cache statuscache.Cache, vault indexVault, marker string) {
	writeFile(
		vault.path, indexTasksDir+"/Task A.md",
		"---\nstatus: next\nassignee: alice\nclaude_session_started: \""+marker+"\"\n---\n# Task A\n",
	)
	Expect(cache.LoadVault(vault.name, vault.path, vault.tasks)).To(Succeed())
}

var _ = Describe("CreateCleanupSweep", func() {
	It("waits for the cache-ready channel before the startup reconcile runs", func() {
		vault := newIndexVault("test")
		loader, configPath := indexLoader(vault)
		cache := statuscache.NewCache()
		// Five minutes old: past the 120s orphan grace, well inside the 45m TTL,
		// so ONLY the startup reconcile can clear it.
		seedTaskMarker(cache, vault, time.Now().UTC().Add(-5*time.Minute).Format(time.RFC3339Nano))

		ready := make(chan struct{})
		runFn := factory.CreateCleanupSweep(
			loader, configPath, cache, launchregistry.NewRegistry(), sessionlock.NewRegistry(),
			tempDir(), ready,
		)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			_ = runFn(ctx)
		}()
		defer func() {
			cancel()
			Eventually(done, "5s").Should(BeClosed())
		}()

		taskFile := indexTasksDir + "/Task A.md"
		By("leaving the marker alone while the cache is not yet loaded")
		Consistently(func() string {
			return frontmatterValue(vault.path, taskFile, "claude_session_started")
		}, "300ms").ShouldNot(BeEmpty())

		By("clearing the orphaned marker once the cache is ready")
		close(ready)
		Eventually(func() string {
			return frontmatterValue(vault.path, taskFile, "claude_session_started")
		}, "5s").Should(BeEmpty())
	})

	It("observes a launch recorded in the registry instance it is handed", func() {
		vault := newIndexVault("test")
		loader, configPath := indexLoader(vault)
		cache := statuscache.NewCache()
		// Older than the TTL: with no registry record the TTL pass would clear it.
		seedTaskMarker(cache, vault, time.Now().UTC().Add(-2*time.Hour).Format(time.RFC3339Nano))

		launches := launchregistry.NewRegistry()
		launches.Begin(vault.name, "Task A", "task")

		ready := make(chan struct{})
		close(ready)
		runFn := factory.CreateCleanupSweep(
			loader, configPath, cache, launches, sessionlock.NewRegistry(), tempDir(), ready,
		)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			_ = runFn(ctx)
		}()
		defer func() {
			cancel()
			Eventually(done, "5s").Should(BeClosed())
		}()

		// A private launchregistry.NewRegistry() would see no record and clear the
		// marker; the shared instance makes both the reconcile and the TTL pass
		// leave the in-flight launch's marker alone.
		Consistently(func() string {
			return frontmatterValue(vault.path, indexTasksDir+"/Task A.md", "claude_session_started")
		}, "500ms").ShouldNot(BeEmpty())
	})

	It("returns a runnable that stops on context cancel", func() {
		vault := newIndexVault("test")
		loader, configPath := indexLoader(vault)
		ready := make(chan struct{})
		close(ready)

		runFn := factory.CreateCleanupSweep(
			loader, configPath, statuscache.NewCache(), launchregistry.NewRegistry(),
			sessionlock.NewRegistry(), tempDir(), ready,
		)
		Expect(runFn).NotTo(BeNil())

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			Expect(runFn(ctx)).To(Succeed())
		}()
		cancel()
		Eventually(done, "5s").Should(BeClosed())
	})

	It("fails when the config cannot be loaded", func() {
		loader, _ := indexLoader()
		runFn := factory.CreateCleanupSweep(
			loader, filepath.Join(tempDir(), "missing.yaml"), statuscache.NewCache(),
			launchregistry.NewRegistry(), sessionlock.NewRegistry(), tempDir(), make(chan struct{}),
		)
		Expect(runFn(context.Background())).To(HaveOccurred())
	})
})
