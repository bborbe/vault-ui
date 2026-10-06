// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	libtime "github.com/bborbe/time"
	vaultmocks "github.com/bborbe/vault-cli/mocks"
	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/ops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/pageindex"
	"github.com/bborbe/vault-ui/pkg/websocket"
)

// watcherHandler builds the watch handler factory.CreateWatcher composes over
// the given index, driven by a fake watch operation that hands the handler out.
func watcherHandler(
	vaults []*config.Vault,
	index pageindex.PageIndex,
) func(ops.WatchEvent) error {
	loader := &vaultmocks.Loader{}
	loader.GetAllVaultsReturns(vaults, nil)
	handlerCh := make(chan func(ops.WatchEvent) error, 1)
	watch := &vaultmocks.WatchOperation{}
	watch.ExecuteStub = func(
		ctx context.Context,
		_ []ops.WatchTarget,
		handler func(ops.WatchEvent) error,
	) error {
		handlerCh <- handler
		<-ctx.Done()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	go func() {
		_ = factory.CreateWatcher(
			loader, websocket.NewConnectionManager(websocket.NewMetrics()), index, watch,
		)(ctx)
	}()
	var handler func(ops.WatchEvent) error
	Eventually(handlerCh, 5*time.Second).Should(Receive(&handler))
	return handler
}

// pageIndexReads returns the vault_ui_page_index_files_read_total value of one
// reason label, scraped the way GET /metrics serves it.
func pageIndexReads(reason string) float64 {
	recorder := httptest.NewRecorder()
	promhttp.Handler().ServeHTTP(
		recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil),
	)
	prefix := `vault_ui_page_index_files_read_total{reason="` + reason + `"} `
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, prefix)), 64)
		Expect(err).NotTo(HaveOccurred())
		return value
	}
	Fail("no " + prefix + "series in the metrics scrape")
	return 0
}

var baseURL string

// freeAddr binds 127.0.0.1:0, reads the port the operating system assigned,
// closes the listener, and returns the address as "127.0.0.1:<port>".
// CreateHTTPServer exposes no way to read back the address it bound, so the port
// must be chosen before the call rather than discovered after it. That leaves an
// inherent race: another process can claim the port between the close here and
// the server's bind. The bind is retried a few times to tolerate a transient
// failure; if no port can be obtained the test fails rather than falling back to
// a fixed port.
func freeAddr() string {
	for attempt := 0; attempt < 5; attempt++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			continue
		}
		addr := listener.Addr().String()
		_ = listener.Close()
		return addr
	}
	Fail("could not obtain a free loopback port for the test server")
	return ""
}

// statusCode performs a GET and returns the HTTP status code, or 0 while the
// server is not yet reachable.
func statusCode(url string) int {
	resp, err := http.Get(url) //nolint:gosec // loopback URL built from a run-time free port in tests
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

// body performs a GET and returns the response body.
func body(url string) string {
	resp, err := http.Get(url) //nolint:gosec // loopback URL built from a run-time free port in tests
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
	addr := freeAddr()
	baseURL = "http://" + addr
	server := factory.CreateHTTPServer(addr, readiness)
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

		It("counts page-index file reads by reason", func() {
			vaultDir := tempDir()
			Expect(os.MkdirAll(filepath.Join(vaultDir, "24 Tasks"), 0750)).To(Succeed())
			names := []string{"One", "Two", "Three"}
			for _, name := range names {
				Expect(os.WriteFile(
					filepath.Join(vaultDir, "24 Tasks", name+".md"),
					[]byte("---\nstatus: next\n---\n# "+name+"\n"), 0600,
				)).To(Succeed())
			}
			vaults := []*config.Vault{{
				Name: "personal", Path: vaultDir, TasksDir: "24 Tasks",
			}}
			index := factory.CreatePageIndex(
				pageindex.NewPageReader(), pageindex.NewDirectoryLister(),
				libtime.NewCurrentDateTime(),
			)
			key := pageindex.NewKey(vaultDir, "24 Tasks")

			beforeBuild := pageIndexReads("build")
			beforeEvent := pageIndexReads("event")

			// A warm build reads every file of the folder exactly once.
			Expect(index.Build(context.Background(), []pageindex.Key{key})).To(Succeed())
			Expect(pageIndexReads("build") - beforeBuild).To(Equal(float64(len(names))))

			// One task event delivered through the handler CreateWatcher builds
			// costs exactly one single-file read.
			handler := watcherHandler(vaults, index)
			Expect(handler(ops.WatchEvent{
				Event: "modified", Name: "One", Vault: "personal",
				Path: filepath.Join("24 Tasks", "One.md"), Type: "task",
			})).To(Succeed())
			Expect(pageIndexReads("event") - beforeEvent).To(Equal(1.0))
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
