// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/mocks"
	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/storage"
	gws "github.com/gorilla/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/queue"
	"github.com/bborbe/vault-ui/pkg/statuscache"
	"github.com/bborbe/vault-ui/pkg/websocket"
)

// newTestAPIHandler wires CreateAPIHandler with a ready gate and a fresh
// connection manager.
func newTestAPIHandler(loader config.Loader, configPath string) http.Handler {
	return newTestAPIHandlerWithManager(
		loader, configPath, websocket.NewConnectionManager(websocket.NewMetrics()),
	)
}

// startWriteQueue returns a fresh write queue whose Consume runs until the spec
// ends, so no in-flight write outlives the spec's temp dir.
func startWriteQueue() queue.Queue {
	writeQueue := factory.CreateWriteQueue()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer GinkgoRecover()
		defer close(done)
		_ = writeQueue.Consume(ctx)
	}()
	DeferCleanup(func() {
		cancel()
		<-done
	})
	return writeQueue
}

// newTestAPIHandlerWithManager wires CreateAPIHandler with a ready gate and the
// given connection manager.
func newTestAPIHandlerWithManager(
	loader config.Loader,
	configPath string,
	manager websocket.ConnectionManager,
) http.Handler {
	readiness := vaultui.NewReadiness()
	readiness.SetReady()
	return factory.CreateAPIHandler(
		loader, configPath, statuscache.NewCache(), factory.CreatePaneResolver(tempDir()),
		launchregistry.NewRegistry(), tempDir(), readiness, manager,
		factory.CreatePageIndex(storage.NewPageStorage(nil), libtime.NewCurrentDateTime()),
		factory.CreateSessionState(),
		startWriteQueue(),
	)
}

func apiFixture() (config.Loader, string, string) {
	vaultDir := tempDir()
	Expect(os.MkdirAll(filepath.Join(vaultDir, "24 Tasks"), 0750)).To(Succeed())
	Expect(os.MkdirAll(filepath.Join(vaultDir, "23 Goals"), 0750)).To(Succeed())
	writeFile(vaultDir, "24 Tasks/Task A.md", "---\nstatus: next\n---\n# Task A\n")
	writeFile(vaultDir, "23 Goals/Goal A.md", "---\nstatus: in_progress\n---\n# Goal A\n")

	configDir := tempDir()
	configPath := filepath.Join(configDir, "config.yaml")
	Expect(os.WriteFile(configPath, []byte("host: 127.0.0.1\n"), 0600)).To(Succeed())

	loader := &mocks.Loader{}
	loader.GetAllVaultsReturns([]*config.Vault{{
		Name:     "personal",
		Path:     vaultDir,
		TasksDir: "24 Tasks",
		GoalsDir: "23 Goals",
	}}, nil)
	loader.GetCurrentUserReturns("tester", nil)
	return loader, configPath, vaultDir
}

var _ = Describe("API factory", func() {
	Describe("CreateAPIListen", func() {
		It("defaults to :8000", func() {
			Expect(os.Unsetenv("VAULT_UI_LISTEN")).To(Succeed())
			Expect(factory.CreateAPIListen()).To(Equal(":8000"))
		})

		It("honours VAULT_UI_LISTEN", func() {
			Expect(os.Setenv("VAULT_UI_LISTEN", "127.0.0.1:12345")).To(Succeed())
			defer func() { _ = os.Unsetenv("VAULT_UI_LISTEN") }()
			Expect(factory.CreateAPIListen()).To(Equal("127.0.0.1:12345"))
		})
	})

	Describe("CreateVaultUIConfigPath", func() {
		It("prefers the XDG path when it exists", func() {
			home := tempDir()
			xdg := filepath.Join(home, ".config", "vault-ui")
			Expect(os.MkdirAll(xdg, 0750)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(xdg, "config.yaml"), []byte(""), 0600)).To(Succeed())
			Expect(factory.CreateVaultUIConfigPath(home)).To(Equal(filepath.Join(xdg, "config.yaml")))
		})

		It("falls back to the legacy repo-root path", func() {
			home := tempDir()
			Expect(os.WriteFile(filepath.Join(home, "config.yaml"), []byte(""), 0600)).To(Succeed())
			Expect(factory.CreateVaultUIConfigPath(home)).To(Equal(filepath.Join(home, "config.yaml")))
		})
	})

	Describe("CreateStaticFS", func() {
		It("serves the frozen frontend tree", func() {
			staticFS := factory.CreateStaticFS()
			content, err := fs.ReadFile(staticFS, "index.html")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("<html"))
			_, err = fs.ReadFile(staticFS, "app.js")
			Expect(err).NotTo(HaveOccurred())
			_, err = fs.ReadFile(staticFS, "style.css")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("CreateAPIHandler", func() {
		It("serves the read routes", func() {
			loader, configPath, _ := apiFixture()
			handler := newTestAPIHandler(loader, configPath)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/vaults", nil))
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("personal"))
		})

		It("serves the frontend at root", func() {
			loader, configPath, _ := apiFixture()
			handler := newTestAPIHandler(loader, configPath)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("<html"))
		})
	})

	Describe("mutating-route publisher wiring", func() {
		It("broadcasts a task update to a connected WebSocket client", func() {
			loader, configPath, _ := apiFixture()
			manager := websocket.NewConnectionManager(websocket.NewMetrics())
			server := httptest.NewServer(newTestAPIHandlerWithManager(loader, configPath, manager))
			defer server.Close()

			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
			conn, _, err := gws.DefaultDialer.Dial(wsURL, nil)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = conn.Close() }()
			Eventually(manager.Count).Should(Equal(1))

			req, err := http.NewRequest(
				http.MethodPatch,
				server.URL+"/api/tasks/Task%20A/flag?vault=personal",
				strings.NewReader(`{"flag":true}`),
			)
			Expect(err).NotTo(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

			_, message, err := conn.ReadMessage()
			Expect(err).NotTo(HaveOccurred())
			var frame map[string]string
			Expect(json.Unmarshal(message, &frame)).To(Succeed())
			Expect(frame).To(Equal(map[string]string{
				"type":      "task_updated",
				"task_id":   "Task A",
				"item_kind": "task",
				"vault":     "personal",
			}))
		})

		It("broadcasts a goal update to a connected WebSocket client", func() {
			loader, configPath, _ := apiFixture()
			manager := websocket.NewConnectionManager(websocket.NewMetrics())
			server := httptest.NewServer(newTestAPIHandlerWithManager(loader, configPath, manager))
			defer server.Close()

			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
			conn, _, err := gws.DefaultDialer.Dial(wsURL, nil)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = conn.Close() }()
			Eventually(manager.Count).Should(Equal(1))

			req, err := http.NewRequest(
				http.MethodPatch,
				server.URL+"/api/goals/Goal%20A/status?vault=personal",
				strings.NewReader(`{"status":"hold"}`),
			)
			Expect(err).NotTo(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

			_, message, err := conn.ReadMessage()
			Expect(err).NotTo(HaveOccurred())
			var frame map[string]string
			Expect(json.Unmarshal(message, &frame)).To(Succeed())
			Expect(frame).To(Equal(map[string]string{
				"type":      "goal_updated",
				"goal_id":   "Goal A",
				"item_kind": "goal",
				"vault":     "personal",
			}))
		})
	})

	Describe("CreateStatusCacheLoader", func() {
		It("loads every vault's statuses", func() {
			loader, configPath, _ := apiFixture()
			cache := statuscache.NewCache()
			Expect(factory.CreateStatusCacheLoader(loader, configPath, cache)(context.Background())).
				To(Succeed())
			status, ok := cache.GetStatus("personal", "Task A")
			Expect(ok).To(BeTrue())
			Expect(status).To(Equal("next"))
		})

		It("fails when the config cannot be loaded", func() {
			loader, _, _ := apiFixture()
			err := factory.CreateStatusCacheLoader(
				loader, filepath.Join(tempDir(), "missing.yaml"), statuscache.NewCache(),
			)(context.Background())
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("CreateAPIServer", func() {
		It("runs and stops on context cancel", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := factory.CreateAPIServer(
				"127.0.0.1:0", http.NewServeMux(),
			)(ctx)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

var _ = Describe("vault-cli pin", func() {
	It("requires v0.159.0 and never replaces it", func() {
		content, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
		Expect(err).NotTo(HaveOccurred())
		text := string(content)
		Expect(text).To(ContainSubstring("github.com/bborbe/vault-cli v0.159.0"))
		for _, line := range strings.Split(text, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "replace") {
				Expect(trimmed).NotTo(ContainSubstring("vault-cli"))
			}
		}
	})
})
