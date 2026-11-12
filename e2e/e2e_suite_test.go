// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build e2e

// Package e2e drives the board in a real browser against a real binary.
//
// The suite never runs automatically: it is not part of `make precommit` and
// not part of CI. `make e2e` is the only entry point, and it needs Chromium and
// a free :9090 (the binary binds the admin port unconditionally).
//
// It shares package e2e with harness.go so the specs can read the harness's
// unexported state — never split this into package e2e_test.
package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mxschmitt/playwright-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	pw        *playwright.Playwright
	browser   playwright.Browser
	vaultPath string
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Vault UI Board E2E Suite")
}

var _ = BeforeSuite(func() {
	ctx := context.Background()

	root, err := os.MkdirTemp("", "vault-ui-e2e-")
	Expect(err).NotTo(HaveOccurred())
	homeDir = root

	repoRoot, err := filepath.Abs("..")
	Expect(err).NotTo(HaveOccurred())

	binaryPath = filepath.Join(root, "vault-ui")
	Expect(buildBinary(ctx, repoRoot, binaryPath)).To(Succeed(), "build failed:\n%s", procStderr)

	vaultPath = filepath.Join(root, "vault")
	Expect(seedFixture(vaultPath)).To(Succeed())
	Expect(writeConfig(homeDir, vaultPath)).To(Succeed())

	port, err := freePort()
	Expect(err).NotTo(HaveOccurred())
	Expect(startBinary(ctx, port)).To(Succeed(), "binary failed to start:\n%s", procStderr)

	pw, err = playwright.Run()
	Expect(err).NotTo(HaveOccurred())
	browser, err = pw.Chromium.Launch()
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	stopBinary()
	if browser != nil {
		_ = browser.Close()
	}
	if pw != nil {
		_ = pw.Stop()
	}
	if homeDir != "" {
		_ = os.RemoveAll(homeDir)
	}
})

// newPage opens path on the served board with a loaded DOM.
func newPage(path string) playwright.Page {
	page, err := browser.NewPage()
	Expect(err).NotTo(HaveOccurred())
	_, err = page.Goto(baseURL+path, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateLoad,
	})
	Expect(err).NotTo(HaveOccurred())
	return page
}
