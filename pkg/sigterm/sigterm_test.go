// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sigterm_test

import (
	"context"
	stderrors "errors"
	"flag"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/sigterm"
)

const (
	pid       = 4242
	sessionID = "e0930886-0843-4ca9-adfa-58819443c032"
)

// spySignaler records every pid it is asked to signal and returns a fixed error.
type spySignaler struct {
	err  error
	pids []int
}

func (s *spySignaler) Signal(pid int) error {
	s.pids = append(s.pids, pid)
	return s.err
}

// captureStderr redirects os.Stderr for the duration of fn and returns what was
// written. glog's stderr sink reads os.Stderr at write time, so a warning can be
// observed without touching the production code.
func captureStderr(fn func()) string {
	old := os.Stderr
	f, err := os.CreateTemp(GinkgoT().TempDir(), "stderr")
	Expect(err).NotTo(HaveOccurred())
	os.Stderr = f
	defer func() { os.Stderr = old }()
	fn()
	os.Stderr = old
	Expect(f.Close()).To(Succeed())
	data, err := os.ReadFile(f.Name())
	Expect(err).NotTo(HaveOccurred())
	return string(data)
}

// warningLines returns every glog line emitted at warning severity or above (a
// glog line starts with its severity letter).
func warningLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "W") {
			lines = append(lines, line)
		}
	}
	return lines
}

// reapedPid starts a short-lived child, waits for it, and returns its pid now
// that the process is gone and reaped.
func reapedPid() int {
	cmd := exec.Command("true")
	Expect(cmd.Start()).To(Succeed())
	pid := cmd.Process.Pid
	Expect(cmd.Wait()).To(Succeed())
	return pid
}

// failureCase drives one DescribeTable row: a synthetic signaler error, whether
// a warning is expected, or the real-signaler dead-pid check.
type failureCase struct {
	err       error
	wantWarn  bool
	realCheck bool
}

var _ = Describe("SigtermPID", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
		// glog's default stderr threshold is ERROR; enable stderr so the
		// warning path can be observed.
		Expect(flag.Set("logtostderr", "true")).To(Succeed())
	})

	It("returns true and signals the pid exactly once", func() {
		signaler := &spySignaler{}

		Expect(sigterm.SigtermPID(ctx, signaler, pid, sessionID)).To(BeTrue())
		Expect(signaler.pids).To(Equal([]int{pid}))
	})

	DescribeTable("classifies signal failures",
		func(tc failureCase) {
			if tc.realCheck {
				err := sigterm.NewProcessSignaler().Signal(reapedPid())

				Expect(stderrors.Is(err, os.ErrProcessDone)).To(BeTrue())
				return
			}

			signaler := &spySignaler{err: tc.err}
			var result bool
			out := captureStderr(func() {
				result = sigterm.SigtermPID(ctx, signaler, pid, sessionID)
			})

			Expect(result).To(BeFalse())
			Expect(signaler.pids).To(Equal([]int{pid}))
			if tc.wantWarn {
				Expect(out).To(ContainSubstring(strconv.Itoa(pid)))
				Expect(out).To(ContainSubstring("permission denied"))
				Expect(warningLines(out)).NotTo(BeEmpty())
			} else {
				// A vanished process is not noisy: no warning is emitted.
				Expect(warningLines(out)).To(BeEmpty())
			}
		},
		Entry("process-gone-between-scan-and-kill", failureCase{err: os.ErrProcessDone}),
		Entry("permission-denied", failureCase{
			err:      &os.SyscallError{Syscall: "kill", Err: syscall.EACCES},
			wantWarn: true,
		}),
		Entry("real-signaler-classifies-a-dead-pid", failureCase{realCheck: true}),
	)
})
