// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/factory"
)

const baseURL = "http://127.0.0.1:9090"

// statusCode performs a GET and returns the HTTP status code, or 0 while the
// server is not yet reachable.
func statusCode(url string) int {
	resp, err := http.Get(url) //nolint:gosec // fixed loopback URL in tests
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

// body performs a GET and returns the response body.
func body(url string) string {
	resp, err := http.Get(url) //nolint:gosec // fixed loopback URL in tests
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	return string(data)
}

// tempDir creates a temp directory removed at the end of the current spec.
func tempDir() string {
	dir, err := os.MkdirTemp("", "vault-ui")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// writeVaultConfig writes a vault-cli config file naming vaultDir as the vault
// "test" and returns the config file path.
func writeVaultConfig(vaultDir string) string {
	configPath := filepath.Join(tempDir(), "config.yaml")
	content := "current_user: alice\n" +
		"default_vault: test\n" +
		"vaults:\n" +
		"  test:\n" +
		"    path: " + vaultDir + "\n" +
		"    name: test\n"
	Expect(os.WriteFile(configPath, []byte(content), 0600)).To(Succeed())
	return configPath
}

var (
	ctx       context.Context
	cancel    context.CancelFunc
	errChan   chan error
	readiness vaultui.Readiness
)

var _ = BeforeEach(func() {
	ctx, cancel = context.WithCancel(context.Background())
	readiness = factory.CreateReadiness()
	errChan = make(chan error, 1)
	server := factory.CreateHTTPServer(":9090", readiness)
	go func() {
		errChan <- server.Run(ctx)
	}()
	Eventually(func() int { return statusCode(baseURL + "/healthz") }, "10s").
		Should(Equal(http.StatusOK))
})

var _ = AfterEach(func() {
	cancel()
	Eventually(errChan, "10s").Should(Receive(BeNil()))
})

var _ = Describe("CreateHTTPServer", func() {
	Context("once the service is ready", func() {
		BeforeEach(func() {
			readiness.SetReady()
		})

		DescribeTable("serves the canonical admin block",
			func(path string) {
				Expect(statusCode(baseURL + path)).To(Equal(http.StatusOK))
			},
			Entry("healthz", "/healthz"),
			Entry("readiness", "/readiness"),
			Entry("metrics", "/metrics"),
			Entry("setloglevel", "/setloglevel/info"),
			Entry("gc", "/gc"),
		)
	})

	Context("readiness", func() {
		It("returns 503 before SetReady", func() {
			Expect(statusCode(baseURL + "/readiness")).To(Equal(http.StatusServiceUnavailable))
		})

		It("returns 200 after SetReady", func() {
			readiness.SetReady()
			Expect(statusCode(baseURL + "/readiness")).To(Equal(http.StatusOK))
		})
	})

	Context("metrics", func() {
		It("exposes the vault-ui build info metric and the Go runtime metrics", func() {
			metrics := body(baseURL + "/metrics")
			Expect(metrics).To(MatchRegexp(`(?m)^vault_ui_build_info`))
			Expect(metrics).To(MatchRegexp(`(?m)^go_`))
		})
	})
})

var _ = Describe("CreateVaultDiscovery", func() {
	It("discovers the configured vault path", func() {
		vaultDir := tempDir()
		loader := factory.CreateConfigLoader(writeVaultConfig(vaultDir))

		path, err := loader.GetVaultPath(ctx, "test")

		Expect(err).NotTo(HaveOccurred())
		Expect(path).To(Equal(vaultDir))
	})

	It("flips readiness 503 -> 200 on successful discovery", func() {
		vaultDir := tempDir()
		loader := factory.CreateConfigLoader(writeVaultConfig(vaultDir))
		Expect(statusCode(baseURL + "/readiness")).
			To(Equal(http.StatusServiceUnavailable))

		Expect(factory.CreateVaultDiscovery(loader, readiness)(ctx)).To(Succeed())

		Expect(statusCode(baseURL + "/readiness")).To(Equal(http.StatusOK))
	})

	It("leaves readiness not-ready when discovery fails", func() {
		configPath := filepath.Join(tempDir(), "config.yaml")
		Expect(os.WriteFile(configPath, []byte("vaults: [\n"), 0600)).To(Succeed())
		loader := factory.CreateConfigLoader(configPath)

		Expect(factory.CreateVaultDiscovery(loader, readiness)(ctx)).NotTo(Succeed())

		Expect(readiness.IsReady()).To(BeFalse())
		Expect(statusCode(baseURL + "/readiness")).
			To(Equal(http.StatusServiceUnavailable))
	})
})
