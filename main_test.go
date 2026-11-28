// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main_test

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/gexec"
)

var _ = Describe("Main", func() {
	It("Compiles", func() {
		var err error
		_, err = gexec.Build(".", "-mod=mod", "-buildvcs=false")
		Expect(err).NotTo(HaveOccurred())
	})
})

func TestSuite(t *testing.T) {
	// Deliberately no `time.Local = time.UTC` here. That assignment writes a
	// package-level variable, and the runtime's timer goroutines read the same
	// variable through time.Now() (via time.sendTime) whenever a timer fires — so it
	// races the moment a timer lands during suite setup. The root suite has no
	// time-dependent assertion (its only spec compiles the binary), so it needs no
	// fixed zone. Suites that DO assert on local time should pin it via TZ=UTC in the
	// test environment rather than by writing this variable at runtime.
	format.TruncatedDiff = false
	RegisterFailHandler(Fail)
	suiteConfig, reporterConfig := GinkgoConfiguration()
	suiteConfig.Timeout = 60 * time.Second
	RunSpecs(t, "Main Suite", suiteConfig, reporterConfig)
}
