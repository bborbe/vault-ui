// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session_test

import (
	"context"
	"errors"
	"time"

	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/session"
)

// Real observed `ps` rows (truncated for brevity, flag order preserved): a
// headless launch (--session-id), an interactive resume (--resume <uuid>), a
// resume by name (no uuid), and a bare session (no session flag at all).
const psHeadless = "64387 claude --settings {\"theme\":\"custom:work-green\"} --model x --print " +
	"-n BRO-21903 Check Builds -p /vault-cli:work-on-task " +
	"\"/path/BRO-21903 Check Builds.md\" --non-interactive --output-format json " +
	"--session-id 0bc9bb57-7034-49b5-b73c-70fe0682e953\n"

const psResume = "40794 claude --settings {\"theme\":\"custom:private-blue\"} --model x " +
	"--add-dir /tmp --resume cbe578a1-3338-4c7c-8fb6-f07cb34eda8d\n"

const psResumeByName = "76493 claude --settings {\"theme\":\"custom:private-blue\"} --model x " +
	"--add-dir /tmp --resume boss\n"

const psNoFlag = "18880 claude --settings {\"theme\":\"custom:private-blue\"} --model x --add-dir /tmp\n"

// A `cc-*` launcher row captured live: `-n <name>` is the LAST argument, after
// `--resume <uuid>` — the shape whose name previously never matched because no
// flag followed it.
const psLauncherNameLast = "claude --settings {\"theme\":\"custom:work-green\"} --model x --add-dir /tmp " +
	"--resume ebd4c030-c912-47ef-96f2-5bd4da80d206 -n Check Failed Builds Watcher\n"

var _ = Describe("Process parsing", func() {
	Describe("ParseLiveSessionIDs", func() {
		It("extracts exact resume and session-id matches from claude rows", func() {
			ps := "  PID TTY STAT TIME COMMAND\n" +
				"13862 ?? S 0:00.01 claude --settings {\"theme\":\"x\"} --model claude-opus-5[1m] " +
				"--resume 7cbde4f8-239c-4f3d-92d7-1e550b0afa88 /vault-cli:work-on-task foo\n" +
				"94284 ?? S 0:00.02 claude --settings {} --model deepseek-v4-flash-max[1m] " +
				"--resume c20647e6-ef96-47b8-866b-220f8dca685d\n" +
				"23478 ?? S 0:00.03 some other process --resume a55b44d0-cc04-4740-a5d9-df0a3e462cf4\n" +
				"28430 ?? S 0:00.04 claude --settings {} --print -p 'no resume here'\n"

			Expect(session.ParseLiveSessionIDs(ps)).To(ConsistOf(
				"7cbde4f8-239c-4f3d-92d7-1e550b0afa88",
				"c20647e6-ef96-47b8-866b-220f8dca685d",
			))
		})

		It("ignores non-claude wrappers and headless prints", func() {
			ps := " 94282 bash cc-personal --resume c20647e6-ef96-47b8-866b-220f8dca685d\n" +
				" 94284 claude --model claude-opus-5[1m] --print -p hi\n" +
				" 40075 claude --settings {} --model deepseek[1m] --resume " +
				"5df6f0a9-927d-4a99-84f8-ce9ff2350ec5\n"

			Expect(session.ParseLiveSessionIDs(ps)).To(ConsistOf("5df6f0a9-927d-4a99-84f8-ce9ff2350ec5"))
		})

		It("counts a headless --session-id launch as liveness", func() {
			Expect(session.ParseLiveSessionIDs(psHeadless)).To(ConsistOf(freshID))
		})

		It("still counts an interactive --resume uuid", func() {
			Expect(session.ParseLiveSessionIDs(psResume)).To(ConsistOf("cbe578a1-3338-4c7c-8fb6-f07cb34eda8d"))
		})

		It("ignores no-flag, launcher-wrapper, and resume-by-name rows", func() {
			ps := psHeadless + psResume + psResumeByName + psNoFlag +
				" 94282 bash cc-personal --resume c20647e6-ef96-47b8-866b-220f8dca685d\n"

			Expect(session.ParseLiveSessionIDs(ps)).To(ConsistOf(
				"0bc9bb57-7034-49b5-b73c-70fe0682e953",
				"cbe578a1-3338-4c7c-8fb6-f07cb34eda8d",
			))
		})
	})

	Describe("ParseLiveSessionNames", func() {
		It("maps a multi-word name to its session id", func() {
			Expect(session.ParseLiveSessionNames(psHeadless)).To(Equal(map[string]string{
				"BRO-21903 Check Builds": "0bc9bb57-7034-49b5-b73c-70fe0682e953",
			}))
		})

		It("requires both flags on the same row", func() {
			ps := psHeadless + psResume + psResumeByName + psNoFlag

			Expect(session.ParseLiveSessionNames(ps)).To(Equal(map[string]string{
				"BRO-21903 Check Builds": "0bc9bb57-7034-49b5-b73c-70fe0682e953",
			}))
		})

		It("omits a name bound to two different uuids", func() {
			ps := psHeadless + " 77112 claude --settings {} --model x --print -n BRO-21903 Check Builds " +
				"--session-id cbe578a1-3338-4c7c-8fb6-f07cb34eda8d\n"

			Expect(session.ParseLiveSessionNames(ps)).To(BeEmpty())
		})

		It("maps a launcher row whose name is the final argument", func() {
			Expect(session.ParseLiveSessionNames(psLauncherNameLast)).To(Equal(map[string]string{
				"Check Failed Builds Watcher": "ebd4c030-c912-47ef-96f2-5bd4da80d206",
			}))
		})

		It("tolerates trailing whitespace after the final name", func() {
			ps := psLauncherNameLast[:len(psLauncherNameLast)-1] + "   \n"

			Expect(session.ParseLiveSessionNames(ps)).To(Equal(map[string]string{
				"Check Failed Builds Watcher": "ebd4c030-c912-47ef-96f2-5bd4da80d206",
			}))
		})

		It("captures no name for a bare trailing -n", func() {
			Expect(session.ParseLiveSessionNames(
				"claude --settings {} --model x --print --session-id ebd4c030-c912-47ef-96f2-5bd4da80d206 -n\n",
			)).To(BeEmpty())
			Expect(session.ParseLiveSessionNames(
				"claude --settings {} --model x --print --session-id ebd4c030-c912-47ef-96f2-5bd4da80d206 -n \n",
			)).To(BeEmpty())
		})

		It("captures no name when -n is directly followed by a flag", func() {
			Expect(session.ParseLiveSessionNames(
				"claude --settings {} --model x --print -n -p /vault-cli:work-on-task " +
					"\"/path/Task.md\" --session-id ebd4c030-c912-47ef-96f2-5bd4da80d206\n",
			)).To(BeEmpty())
			Expect(session.ParseLiveSessionNames(
				"claude --settings {} --model x --print --session-id ebd4c030-c912-47ef-96f2-5bd4da80d206 -n -p\n",
			)).To(BeEmpty())
		})
	})

	Describe("ParseLiveProcesses", func() {
		It("maps session id to pid for claude matches", func() {
			ps := " 12345 claude --settings {} --model claude-opus-5[1m] --resume " +
				"7cbde4f8-239c-4f3d-92d7-1e550b0afa88 /vault-cli:work-on-task foo\n" +
				" 67890 claude --settings {} --model deepseek-v4-flash-max[1m] --resume " +
				"c20647e6-ef96-47b8-866b-220f8dca685d\n" +
				" 23478 bash cc-personal --resume a55b44d0-cc04-4740-a5d9-df0a3e462cf4\n" +
				" 28430 claude --settings {} --print -p 'no resume here'\n"

			Expect(session.ParseLiveProcesses(ps)).To(Equal(map[string]int{
				"7cbde4f8-239c-4f3d-92d7-1e550b0afa88": 12345,
				"c20647e6-ef96-47b8-866b-220f8dca685d": 67890,
			}))
		})

		It("finds a headless --session-id row", func() {
			Expect(session.ParseLiveProcesses(psHeadless + psResume)).To(Equal(map[string]int{
				"0bc9bb57-7034-49b5-b73c-70fe0682e953": 64387,
				"cbe578a1-3338-4c7c-8fb6-f07cb34eda8d": 40794,
			}))
		})

		It("skips a non-numeric pid prefix instead of failing", func() {
			ps := "claude --settings {} --model claude-opus-5[1m] --resume " +
				"7cbde4f8-239c-4f3d-92d7-1e550b0afa88\n"

			Expect(session.ParseLiveProcesses(ps)).To(BeEmpty())
		})
	})

	Describe("ParseLaunchProcesses and ParseLaunchNames", func() {
		It("counts only --session-id rows as launches", func() {
			ps := " 43177 bash cc-personal --resume 78912169-01b2-4601-a45d-c9ae0d258efb\n" +
				" 43205 claude --settings {} --print -n Some Task -p /vault-cli:work-on-task " +
				"--session-id a978980b-d1f7-4d1d-a970-1f9118916374\n"

			Expect(session.ParseLaunchProcesses(ps)).To(Equal(map[string]int{
				"a978980b-d1f7-4d1d-a970-1f9118916374": 43205,
			}))
			Expect(session.ParseLaunchNames(ps)).To(Equal(map[string]string{
				"Some Task": "a978980b-d1f7-4d1d-a970-1f9118916374",
			}))
		})
	})

	Describe("ProcessTable", func() {
		var (
			ctx             context.Context
			currentDateTime libtime.CurrentDateTime
			calls           int
			scanner         session.ProcessScanner
		)

		BeforeEach(func() {
			ctx = context.Background()
			currentDateTime = libtime.NewCurrentDateTime()
			currentDateTime.SetNow(libtime.NewDateTime(2026, time.October, 4, 12, 0, 0, 0, time.UTC))
			calls = 0
			scanner = func(ctx context.Context) (string, error) {
				calls++
				return psHeadless, nil
			}
		})

		It("scans once and serves both derived views from the cached scan", func() {
			table := session.NewProcessTable(scanner, 30*time.Second, currentDateTime)

			Expect(table.LiveSessionIDs(ctx)).To(ConsistOf(freshID))
			Expect(table.LiveSessionNames(ctx)).To(Equal(map[string]string{
				"BRO-21903 Check Builds": freshID,
			}))
			Expect(calls).To(Equal(1))
		})

		It("re-scans once the cache is older than its TTL", func() {
			table := session.NewProcessTable(scanner, 30*time.Second, currentDateTime)

			table.LiveSessionIDs(ctx)
			currentDateTime.SetNow(currentDateTime.Now().Add(libtime.Duration(31 * time.Second)))
			table.LiveSessionIDs(ctx)

			Expect(calls).To(Equal(2))
		})

		It("treats a scanner error as an empty cached table", func() {
			scanner = func(ctx context.Context) (string, error) {
				calls++
				return "", errors.New("ps exploded")
			}
			table := session.NewProcessTable(scanner, 30*time.Second, currentDateTime)

			Expect(table.LiveSessionIDs(ctx)).To(BeEmpty())
			Expect(table.LiveSessionNames(ctx)).To(BeEmpty())
			Expect(calls).To(Equal(1))
		})
	})

	Describe("NewPSScanner", func() {
		It("runs ps and returns its output", func() {
			output, err := session.NewPSScanner("-axww", "-o", "args=")(context.Background())

			Expect(err).NotTo(HaveOccurred())
			Expect(output).NotTo(BeEmpty())
		})

		It("wraps a ps failure as an error", func() {
			_, err := session.NewPSScanner("--definitely-not-a-ps-flag")(context.Background())

			Expect(err).To(HaveOccurred())
		})
	})
})
