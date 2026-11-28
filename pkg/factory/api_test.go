// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing/fstest"

	"github.com/bborbe/vault-cli/mocks"
	"github.com/bborbe/vault-cli/pkg/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/events"
	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/queue"
	"github.com/bborbe/vault-ui/pkg/sessionlock"
	"github.com/bborbe/vault-ui/pkg/vaultapi"
	"github.com/bborbe/vault-ui/pkg/websocket"
)

// newTestAPIHandler wires CreateAPIHandler with a ready gate and a fresh
// connection manager.
func newTestAPIHandler(
	loader config.Loader, configPath string, source *vaultapi.Source,
) http.Handler {
	return newTestAPIHandlerWithManager(
		loader, configPath, websocket.NewConnectionManager(websocket.NewMetrics()),
		events.NewHub(), source,
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
	hub *events.Hub,
	source *vaultapi.Source,
) http.Handler {
	readiness := vaultui.NewReadiness()
	readiness.SetReady()
	return factory.CreateAPIHandler(
		loader, configPath, factory.CreatePaneResolver(tempDir()),
		launchregistry.NewRegistry(), sessionlock.NewRegistry(), tempDir(), readiness, manager,
		hub,
		factory.CreateSessionSnapshot(factory.CreateSessionState()),
		startWriteQueue(),
		source,
		nil,
		testDist(),
	)
}

// testDist is a minimal frontend tree. The served bytes are not what these
// specs assert; the router only needs a readable FS.
func testDist() fs.FS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><html></html>")},
	}
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
		It("serves the tree it is given, rooted at the dist directory", func() {
			staticFS := factory.CreateStaticFS(testDist())
			content, err := fs.ReadFile(staticFS, "index.html")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("<html"))
		})
	})

	Describe("CreateAPIHandler", func() {
		It("serves the read routes", func() {
			loader, configPath, _ := apiFixture()
			handler := newTestAPIHandler(loader, configPath, vaultapi.NewSourceFromData(nil, nil, nil, nil))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/vaults", nil))
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("personal"))
		})

		It("serializes the source field for all three coverage states", func() {
			loader, configPath, _ := apiFixture()
			cases := []struct {
				name   string
				vaults []string
				want   any
			}{
				{"unknown", nil, nil},
				{"served", []string{"personal"}, true},
				{"unserved", []string{"other"}, false},
			}
			for _, tc := range cases {
				handler := newTestAPIHandler(
					loader, configPath,
					vaultapi.NewSourceFromData(nil, nil, nil, tc.vaults),
				)
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/vaults", nil))
				Expect(recorder.Code).To(Equal(http.StatusOK), tc.name)

				var body []map[string]any
				Expect(json.Unmarshal(recorder.Body.Bytes(), &body)).To(Succeed(), tc.name)
				Expect(body).To(HaveLen(1), tc.name)
				value, present := body[0]["source"]
				Expect(present).To(BeTrue(), "%s: source key must always be emitted", tc.name)
				if tc.want == nil {
					Expect(value).To(BeNil(), "%s: source must serialize as null", tc.name)
				} else {
					Expect(value).To(Equal(tc.want), tc.name)
				}
			}
		})

		It("serves the frontend at root", func() {
			loader, configPath, _ := apiFixture()
			handler := newTestAPIHandler(loader, configPath, vaultapi.NewSourceFromData(nil, nil, nil, nil))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("<html"))
		})
	})

	Describe("mutating-route publisher wiring", func() {
		It("wakes the SSE stream after a mutation", func() {
			loader, configPath, _ := apiFixture()
			hub := events.NewHub()
			// The notifying manager is the production wiring: every Broadcast
			// also wakes the hub, and the stream is the only live transport.
			manager := factory.CreateNotifyingManager(
				websocket.NewConnectionManager(websocket.NewMetrics()), hub,
			)
			server := httptest.NewServer(newTestAPIHandlerWithManager(
				loader, configPath, manager, hub,
				vaultapi.NewSourceFromData(nil, nil, nil, nil),
			))
			defer server.Close()

			stream, err := http.Get(server.URL + "/api/events")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = stream.Body.Close() }()
			Expect(stream.StatusCode).To(Equal(http.StatusOK))
			Expect(stream.Header.Get("Content-Type")).To(Equal("text/event-stream"))

			// Count board events on the stream. The initial event proves the
			// stream is live; a second one after the mutation proves the
			// publisher reached the hub.
			events := &eventCounter{}
			go events.consume(stream.Body)
			Eventually(events.count, "5s").Should(BeNumerically(">=", 1))

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

			Eventually(events.count, "5s").Should(BeNumerically(">=", 2),
				"a mutation must wake the stream")
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

// eventCounter counts `event: board` lines on an SSE stream. The stream is
// read from its own goroutine, so the count is mutex-guarded.
type eventCounter struct {
	mu    sync.Mutex
	board int
}

func (c *eventCounter) consume(body io.Reader) {
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		if scanner.Text() == "event: board" {
			c.mu.Lock()
			c.board++
			c.mu.Unlock()
		}
	}
}

func (c *eventCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.board
}

var _ = Describe("vault-cli pin", func() {
	It("requires v0.200.7 and never replaces it", func() {
		content, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
		Expect(err).NotTo(HaveOccurred())
		text := string(content)
		Expect(text).To(ContainSubstring("github.com/bborbe/vault-cli v0.200.7"))
		for _, line := range strings.Split(text, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "replace") {
				Expect(trimmed).NotTo(ContainSubstring("vault-cli"))
			}
		}
	})
})
