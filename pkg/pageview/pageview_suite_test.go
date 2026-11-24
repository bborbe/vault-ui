// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageview_test

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestPageView is the Ginkgo suite entry point. Without it every Ginkgo node in
// this package is silently skipped — a green `go test` that proves nothing.
func TestPageView(t *testing.T) {
	RegisterFailHandler(Fail)
	suiteConfig, reporterConfig := GinkgoConfiguration()
	suiteConfig.Timeout = 2 * time.Minute
	RunSpecs(t, "PageView Suite", suiteConfig, reporterConfig)
}
