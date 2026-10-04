// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/bborbe/vault-cli/mocks"
	"github.com/bborbe/vault-cli/pkg/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/statuscache"
)

func apiFixture() (config.Loader, string, string) {
	vaultDir := tempDir()
	Expect(os.MkdirAll(filepath.Join(vaultDir, "24 Tasks"), 0750)).To(Succeed())
	writeFile(vaultDir, "24 Tasks/Task A.md", "---\nstatus: next\n---\n# Task A\n")

	configDir := tempDir()
	configPath := filepath.Join(configDir, "config.yaml")
	Expect(os.WriteFile(configPath, []byte("host: 127.0.0.1\n"), 0600)).To(Succeed())

	loader := &mocks.Loader{}
	loader.GetAllVaultsReturns([]*config.Vault{{
		Name:     "personal",
		Path:     vaultDir,
		TasksDir: "24 Tasks",
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
			handler := factory.CreateAPIHandler(
				loader, configPath, statuscache.NewCache(), launchregistry.NewRegistry(), tempDir(),
			)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/vaults", nil))
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("personal"))
		})

		It("serves the frontend at root", func() {
			loader, configPath, _ := apiFixture()
			handler := factory.CreateAPIHandler(
				loader, configPath, statuscache.NewCache(), launchregistry.NewRegistry(), tempDir(),
			)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("<html"))
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
