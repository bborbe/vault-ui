// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package terminate_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/session"
	"github.com/bborbe/vault-ui/pkg/terminate"
)

// Real observed `ps -o pid=,args=` rows (truncated for brevity, flag order
// preserved). sessionID is the card's own frontmatter id; launchID is the fresh
// uuid a relaunch pins, resolvable only through `-n <title>`.
const (
	sessionID = "0bc9bb57-7034-49b5-b73c-70fe0682e953"
	launchID  = "7e486b43-535c-4aac-8e7b-bcaae7b3ab89"
	staleID   = "769563ff-e2ab-40f8-a0db-4098e7d72756"
)

// A launch row that pins the card's own id, carrying the `-n Some Task` name.
const psLaunchPinningCardID = "43205 claude --settings {} --print -n Some Task " +
	"-p /vault-cli:work-on-task --session-id " + sessionID + "\n"

// A launch row pinning a fresh uuid, resolvable only by its `-n` title.
const psLaunchByName = "7748 claude --settings {} --print -n Blocked-by dependencies " +
	"-p /vault-cli:work-on-task --session-id " + launchID + "\n"

// An interactive resume pinning the card's own id — never a launch target.
const psResumeOnly = "40794 claude --settings {} --model x --add-dir /tmp --resume " +
	sessionID + "\n"

// A claude row with no session-pinning flag at all.
const psNoMatch = "18880 claude --settings {} --model x --add-dir /tmp\n"

// spySignaler records every pid it is asked to signal.
type spySignaler struct {
	pids []int
}

func (s *spySignaler) Signal(pid int) error {
	s.pids = append(s.pids, pid)
	return nil
}

// scanner returns a ProcessScanner serving a fixed process table.
func scanner(ps string) session.ProcessScanner {
	return func(ctx context.Context) (string, error) {
		return ps, nil
	}
}

// guardCase drives one DescribeTable row through TerminateLaunchProcess.
type guardCase struct {
	ps        string
	sessionID string
	itemName  string
	wantID    string
	wantTerm  bool
	wantPids  []int
}

var _ = Describe("TerminateResumedSession", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("kills a matched process and returns true", func() {
		signaler := &spySignaler{}

		Expect(terminate.TerminateResumedSession(
			ctx, scanner(psLaunchPinningCardID), signaler, sessionID,
		)).To(BeTrue())
		Expect(signaler.pids).To(Equal([]int{43205}))
	})

	It("returns false with no signal for no match", func() {
		signaler := &spySignaler{}

		Expect(terminate.TerminateResumedSession(
			ctx, scanner(psNoMatch), signaler, sessionID,
		)).To(BeFalse())
		Expect(signaler.pids).To(BeEmpty())
	})
})

var _ = Describe("TerminateLaunchProcess", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	DescribeTable("applies the launch guard",
		func(tc guardCase) {
			signaler := &spySignaler{}

			id, terminated := terminate.TerminateLaunchProcess(
				ctx, scanner(tc.ps), signaler, tc.sessionID, tc.itemName,
			)

			Expect(id).To(Equal(tc.wantID))
			Expect(terminated).To(Equal(tc.wantTerm))
			Expect(signaler.pids).To(Equal(tc.wantPids))
		},
		Entry("matching-row-signals-once", guardCase{
			ps:        psLaunchPinningCardID,
			sessionID: sessionID,
			itemName:  "Some Task",
			wantID:    sessionID,
			wantTerm:  true,
			wantPids:  []int{43205},
		}),
		Entry("no-match-signals-nothing", guardCase{
			ps:        psNoMatch,
			sessionID: sessionID,
			itemName:  "Some Task",
			wantID:    sessionID,
			wantTerm:  false,
			wantPids:  nil,
		}),
		Entry("resume-only-row-signals-nothing", guardCase{
			ps:        psResumeOnly,
			sessionID: sessionID,
			itemName:  "Some Task",
			wantID:    sessionID,
			wantTerm:  false,
			wantPids:  nil,
		}),
	)

	It("resolves by -n <title> when the card's id has no process", func() {
		signaler := &spySignaler{}

		id, terminated := terminate.TerminateLaunchProcess(
			ctx, scanner(psLaunchByName), signaler, staleID, "Blocked-by dependencies",
		)

		Expect(id).To(Equal(launchID))
		Expect(terminated).To(BeTrue())
		Expect(signaler.pids).To(Equal([]int{7748}))
	})

	It("resolves by -n <title> when the card id is empty", func() {
		signaler := &spySignaler{}

		id, terminated := terminate.TerminateLaunchProcess(
			ctx, scanner(psLaunchByName), signaler, "", "Blocked-by dependencies",
		)

		Expect(id).To(Equal(launchID))
		Expect(terminated).To(BeTrue())
		Expect(signaler.pids).To(Equal([]int{7748}))
	})

	It("returns the caller's id with false when nothing runs", func() {
		signaler := &spySignaler{}

		id, terminated := terminate.TerminateLaunchProcess(
			ctx, scanner(psNoMatch), signaler, sessionID, "Some Task",
		)

		Expect(id).To(Equal(sessionID))
		Expect(terminated).To(BeFalse())
		Expect(signaler.pids).To(BeEmpty())
	})
})

var _ = Describe("ItemHasLiveLaunch", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("counts a live launch by id and by name", func() {
		Expect(terminate.ItemHasLiveLaunch(
			ctx, scanner(psLaunchByName), launchID, "Blocked-by dependencies",
		)).To(BeTrue())
		Expect(terminate.ItemHasLiveLaunch(
			ctx, scanner(psLaunchByName), launchID, "Other Task",
		)).To(BeTrue())
		Expect(terminate.ItemHasLiveLaunch(
			ctx, scanner(psLaunchByName), staleID, "Other Task",
		)).To(BeFalse())
	})

	It("does not count an interactive resume", func() {
		Expect(terminate.ItemHasLiveLaunch(
			ctx, scanner(psResumeOnly), sessionID, "Some Task",
		)).To(BeFalse())
	})
})
