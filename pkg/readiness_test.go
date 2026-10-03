// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultui_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultui "github.com/bborbe/vault-ui/pkg"
)

var _ = Describe("Readiness", func() {
	var readiness vaultui.Readiness

	BeforeEach(func() {
		readiness = vaultui.NewReadiness()
	})

	It("is not ready when freshly created", func() {
		Expect(readiness.IsReady()).To(BeFalse())
	})

	It("is ready after SetReady", func() {
		readiness.SetReady()
		Expect(readiness.IsReady()).To(BeTrue())
	})
})
