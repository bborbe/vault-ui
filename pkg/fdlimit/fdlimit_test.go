// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fdlimit_test

import (
	"context"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/fdlimit"
)

// loweredSoftLimit is comfortably above the descriptors this test process
// already holds, so lowering the soft limit to it never makes an unrelated
// open fail, yet it stays far below the target Raise computes.
const loweredSoftLimit uint64 = 256

var _ = Describe("TargetLimit", func() {
	// uint64(syscall.RLIM_INFINITY) does not compile on linux, where
	// RLIM_INFINITY is the untyped constant -1: converting it through a typed
	// int keeps the table portable across linux and darwin.
	var inf int = syscall.RLIM_INFINITY

	DescribeTable("computes the descriptor limit to request",
		func(current, hard, expected uint64) {
			Expect(fdlimit.TargetLimit(current, hard)).To(Equal(expected))
		},
		Entry("current below a finite hard", uint64(1024), uint64(4096), uint64(4096)),
		Entry("current equal to hard", uint64(4096), uint64(4096), uint64(4096)),
		Entry("infinite hard caps at MaxLimit", uint64(1024), uint64(inf), fdlimit.MaxLimit),
		Entry("current above the cap is never lowered", uint64(70000), uint64(70000), uint64(70000)),
		Entry("current equal to the cap stays at the cap", fdlimit.MaxLimit, fdlimit.MaxLimit, fdlimit.MaxLimit),
		Entry("current above a finite hard is never lowered", uint64(8192), uint64(4096), uint64(8192)),
	)
})

var _ = Describe("Raise", func() {
	var (
		ctx        context.Context
		original   syscall.Rlimit
		loweredTo  syscall.Rlimit
		hardLimit  uint64
		restoreErr error
	)

	BeforeEach(func() {
		ctx = context.Background()
		Expect(syscall.Getrlimit(syscall.RLIMIT_NOFILE, &original)).To(Succeed())
		hardLimit = original.Max

		loweredTo = original
		loweredTo.Cur = loweredSoftLimit
		Expect(syscall.Setrlimit(syscall.RLIMIT_NOFILE, &loweredTo)).To(Succeed())
	})

	AfterEach(func() {
		restoreErr = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &original)
		Expect(restoreErr).NotTo(HaveOccurred())
	})

	It("raises the real soft limit above the lowered value", func() {
		applied, err := fdlimit.Raise(ctx)

		Expect(err).NotTo(HaveOccurred())
		Expect(applied).To(Equal(fdlimit.TargetLimit(loweredSoftLimit, hardLimit)))

		var after syscall.Rlimit
		Expect(syscall.Getrlimit(syscall.RLIMIT_NOFILE, &after)).To(Succeed())
		Expect(after.Cur).To(BeNumerically(">", loweredSoftLimit))
	})
})
