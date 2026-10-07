// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package activity_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/activity"
)

const sessionID = "e0930886-0843-4ca9-adfa-58819443c032"

// baseTime is the fixed clock the activity tests run against; every transcript
// mtime and every modified date is derived from it, so the suite has no
// wall-clock dependency.
var baseTime = time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)

// writeTranscript writes a `<sessionID>.jsonl` file whose mtime is `age` before
// baseTime, creating the directory if needed.
func writeTranscript(directory, id string, age time.Duration) {
	Expect(os.MkdirAll(directory, 0o750)).To(Succeed())
	path := filepath.Join(directory, id+".jsonl")
	Expect(os.WriteFile(path, []byte("{\"type\":\"mode\"}\n"), 0o600)).To(Succeed())
	when := baseTime.Add(-age)
	Expect(os.Chtimes(path, when, when)).To(Succeed())
}

// writeRegistryEntry writes one `<pid>.json` session-registry entry as the
// harness writes it (a real payload, not a bare stub).
func writeRegistryEntry(directory string, id string, pid int) {
	Expect(os.MkdirAll(directory, 0o750)).To(Succeed())
	payload, err := json.Marshal(map[string]any{
		"pid":       pid,
		"sessionId": id,
		"cwd":       "/Users/someone/Documents/vault",
		"status":    "idle",
		"name":      "some task",
	})
	Expect(err).NotTo(HaveOccurred())
	path := filepath.Join(directory, strconv.Itoa(pid)+".json")
	Expect(os.WriteFile(path, payload, 0o600)).To(Succeed())
}

var _ = Describe("Activity", func() {
	var (
		ctx         context.Context
		tmp         string
		projects    string
		projectDir  string
		transcripts string
	)

	BeforeEach(func() {
		ctx = context.Background()
		tmp = GinkgoT().TempDir()
		projects = filepath.Join(tmp, "projects")
		projectDir = filepath.Join(projects, "-vault")
		transcripts = projects
	})

	DescribeTable("ComputeActivityDate",
		func(setup func() (*libtime.DateTime, string, string, string), expected func(*libtime.DateTime) *libtime.DateTime) {
			modified, id, dir, root := setup()
			Expect(
				activity.ComputeActivityDate(ctx, modified, id, dir, root),
			).To(Equal(expected(modified)))
		},
		Entry("newer-signal-wins",
			func() (*libtime.DateTime, string, string, string) {
				writeTranscript(projectDir, sessionID, 3*time.Hour)
				modified := libtime.DateTime(baseTime.Add(-30 * time.Second)).UTC()
				return modified.Ptr(), sessionID, projectDir, transcripts
			},
			func(modified *libtime.DateTime) *libtime.DateTime {
				return modified
			},
		),
		Entry("neither-signal-is-none",
			func() (*libtime.DateTime, string, string, string) {
				Expect(os.MkdirAll(projects, 0o750)).To(Succeed())
				return nil, "", filepath.Join(projects, "-vault"), projects
			},
			func(*libtime.DateTime) *libtime.DateTime {
				return nil
			},
		),
	)

	It("finds the transcript in the project dir", func() {
		writeTranscript(projectDir, sessionID, 3*time.Minute)

		result := activity.TranscriptMtime(ctx, sessionID, projectDir, transcripts)

		Expect(result).NotTo(BeNil())
		Expect(result.Time()).To(BeTemporally("~", baseTime.Add(-3*time.Minute), time.Second))
	})

	It("finds the transcript via the projects-root glob fallback", func() {
		Expect(os.MkdirAll(projectDir, 0o750)).To(Succeed())
		writeTranscript(filepath.Join(projects, "-Users-someone-code-repo"), sessionID, 5*time.Minute)

		result := activity.TranscriptMtime(ctx, sessionID, projectDir, transcripts)

		Expect(result).NotTo(BeNil())
	})

	It("returns nil for a missing transcript", func() {
		Expect(os.MkdirAll(projects, 0o750)).To(Succeed())

		Expect(
			activity.TranscriptMtime(ctx, sessionID, filepath.Join(projects, "nope"), projects),
		).To(BeNil())
	})

	It("returns nil for a missing or blank session id", func() {
		Expect(activity.TranscriptMtime(ctx, "", tmp, tmp)).To(BeNil())
	})

	It("prefers a fresh transcript over a stale file", func() {
		writeTranscript(projectDir, sessionID, 30*time.Second)
		staleFile := libtime.DateTime(baseTime.Add(-4 * time.Hour)).UTC()

		result := activity.ComputeActivityDate(ctx, staleFile.Ptr(), sessionID, projectDir, transcripts)

		Expect(result).NotTo(BeNil())
		Expect(result.Time()).To(BeTemporally("~", baseTime.Add(-30*time.Second), time.Second))
	})

	It("prefers a fresh file over a dead transcript", func() {
		writeTranscript(projectDir, sessionID, 3*time.Hour)
		freshFile := libtime.DateTime(baseTime.Add(-2 * time.Minute)).UTC()

		result := activity.ComputeActivityDate(ctx, freshFile.Ptr(), sessionID, projectDir, transcripts)

		Expect(result).NotTo(BeNil())
		Expect(result.Time()).To(BeTemporally("~", freshFile.Time(), time.Second))
	})

	It("falls back to the file mtime when there is no session id", func() {
		Expect(os.MkdirAll(projects, 0o750)).To(Succeed())
		modified := libtime.DateTime(baseTime.Add(-48 * time.Hour)).UTC()

		result := activity.ComputeActivityDate(ctx, modified.Ptr(), "", filepath.Join(projects, "-vault"), projects)

		Expect(result).NotTo(BeNil())
		Expect(result.Equal(modified)).To(BeTrue())
	})

	It("accepts a non-UTC modified date and normalises it to UTC", func() {
		Expect(os.MkdirAll(projects, 0o750)).To(Succeed())
		// Go's time.Time always carries a location (a zero location panics), so
		// the "naive" Python datetime has no direct analogue: a fixed-zone value
		// is the closest seam, and the port must normalise it to UTC.
		modified := libtime.DateTime(baseTime.In(time.FixedZone("UTC+2", 2*60*60)))

		result := activity.ComputeActivityDate(ctx, modified.Ptr(), "", filepath.Join(projects, "-vault"), projects)

		Expect(result).NotTo(BeNil())
		Expect(result.Time().Location()).To(Equal(time.UTC))
		Expect(result.Equal(modified)).To(BeTrue())
	})

	Describe("TranscriptIndex", func() {
		// missingDir is a projectDir with no transcript, so a lookup falls
		// through to the index rather than resolving on the direct stat.
		missingDir := func() string { return filepath.Join(tmp, "no-such-project") }

		// index builds a listing the spec expects to have completed, so a spec
		// that accidentally exercises the abandoned path fails loudly instead
		// of quietly asserting against a truncated index.
		index := func() *activity.TranscriptIndex {
			built, complete := activity.NewTranscriptIndex(ctx, projects)
			Expect(complete).To(BeTrue())
			return built
		}

		It("resolves the same mtime as TranscriptMtime, including from another project dir", func() {
			writeTranscript(projectDir, sessionID, time.Minute)

			direct := activity.TranscriptMtime(ctx, sessionID, missingDir(), projects)
			Expect(direct).NotTo(BeNil())

			Expect(
				index().Mtime(ctx, sessionID, missingDir()),
			).To(Equal(direct))
		})

		It("reports an unknown session absent, exactly as the glob fallback does", func() {
			writeTranscript(projectDir, sessionID, time.Minute)
			unknown := "11111111-1111-1111-1111-111111111111"

			Expect(
				index().Mtime(ctx, unknown, missingDir()),
			).To(BeNil())
			Expect(activity.TranscriptMtime(ctx, unknown, missingDir(), projects)).To(BeNil())
		})

		It("returns nil for a blank session id", func() {
			Expect(index().Mtime(ctx, "", projectDir)).To(BeNil())
		})

		It("prefers projectDir and pins the mtime it prefers", func() {
			writeTranscript(projectDir, sessionID, time.Hour)
			writeTranscript(filepath.Join(projects, "-elsewhere"), sessionID, time.Minute)

			// A concrete expectation rather than equality with TranscriptMtime:
			// two equally-wrong answers would otherwise agree with each other.
			Expect(
				index().Mtime(ctx, sessionID, projectDir),
			).To(Equal(libtime.DateTime(baseTime.Add(-time.Hour)).UTC().Ptr()))
		})

		It("resolves a session in two project dirs to the lexicographically first, as the glob did", func() {
			// "-aaa" sorts before "-vault", so Glob's sorted match order picked
			// it; the index walks directories in the same order.
			writeTranscript(filepath.Join(projects, "-aaa"), sessionID, time.Hour)
			writeTranscript(projectDir, sessionID, time.Minute)

			Expect(
				index().Mtime(ctx, sessionID, missingDir()),
			).To(Equal(libtime.DateTime(baseTime.Add(-time.Hour)).UTC().Ptr()))
		})

		It("follows a symlinked project directory, as the glob did", func() {
			real := filepath.Join(tmp, "real-project-dir")
			writeTranscript(real, sessionID, time.Minute)
			Expect(os.MkdirAll(projects, 0o750)).To(Succeed())
			Expect(os.Symlink(real, filepath.Join(projects, "linked"))).To(Succeed())

			indexed := index().Mtime(ctx, sessionID, missingDir())
			Expect(indexed).NotTo(BeNil())
			Expect(indexed).To(Equal(
				activity.TranscriptMtime(ctx, sessionID, missingDir(), projects),
			))
		})

		It("answers from an empty index when the root cannot be listed", func() {
			built, complete := activity.NewTranscriptIndex(ctx, filepath.Join(tmp, "does-not-exist"))
			Expect(complete).To(BeTrue(), "an unreadable root is a complete listing of nothing")
			Expect(built.Mtime(ctx, sessionID, projectDir)).To(BeNil())
		})

		It("reports an abandoned listing incomplete, not as a full one", func() {
			writeTranscript(projectDir, sessionID, time.Minute)
			cancelled, cancel := context.WithCancel(ctx)
			cancel()

			built, complete := activity.NewTranscriptIndex(cancelled, projects)
			Expect(complete).To(BeFalse(),
				"a caller that caches this listing would misreport every session it did not reach")
			Expect(built.Mtime(cancelled, sessionID, missingDir())).To(BeNil())
		})
	})

	Describe("ComputeActivityDateWith", func() {
		probeReturning := func(value *libtime.DateTime) activity.TranscriptMtimeGetter {
			return func(_ context.Context, _, _, _ string) *libtime.DateTime { return value }
		}

		It("uses the injected probe instead of the filesystem", func() {
			// No transcript exists on disk; only the probe supplies an mtime.
			probed := libtime.DateTime(baseTime.Add(-time.Minute)).UTC()
			modified := libtime.DateTime(baseTime.Add(-2 * time.Hour)).UTC()

			result := activity.ComputeActivityDateWith(
				ctx, probeReturning(probed.Ptr()), modified.Ptr(), sessionID, projectDir, transcripts,
			)

			Expect(result).NotTo(BeNil())
			Expect(result.Equal(probed)).To(BeTrue())
		})

		It("returns the newer of the task mtime and the probed mtime", func() {
			probed := libtime.DateTime(baseTime.Add(-2 * time.Hour)).UTC()
			modified := libtime.DateTime(baseTime.Add(-time.Minute)).UTC()

			result := activity.ComputeActivityDateWith(
				ctx, probeReturning(probed.Ptr()), modified.Ptr(), sessionID, projectDir, transcripts,
			)

			Expect(result).NotTo(BeNil())
			Expect(result.Equal(modified)).To(BeTrue())
		})

		It("treats a nil probe as TranscriptMtime", func() {
			writeTranscript(projectDir, sessionID, 30*time.Second)
			staleFile := libtime.DateTime(baseTime.Add(-4 * time.Hour)).UTC()

			result := activity.ComputeActivityDateWith(
				ctx, nil, staleFile.Ptr(), sessionID, projectDir, transcripts,
			)

			Expect(result).NotTo(BeNil())
			Expect(result.Time()).To(BeTemporally("~", baseTime.Add(-30*time.Second), time.Second))
		})

		It("leaves ComputeActivityDate equal to the injected default", func() {
			writeTranscript(projectDir, sessionID, 3*time.Hour)
			modified := libtime.DateTime(baseTime.Add(-30 * time.Minute)).UTC()

			Expect(
				activity.ComputeActivityDate(ctx, modified.Ptr(), sessionID, projectDir, transcripts),
			).To(Equal(activity.ComputeActivityDateWith(
				ctx, activity.TranscriptMtime, modified.Ptr(), sessionID, projectDir, transcripts,
			)))
		})
	})

	Describe("ReadRegistrySessionIDs", func() {
		It("returns the session ids", func() {
			registryRoot := filepath.Join(tmp, "sessions")
			writeRegistryEntry(registryRoot, sessionID, 1)
			writeRegistryEntry(registryRoot, "0bc9bb57-7034-49b5-b73c-70fe0682e953", 2)

			Expect(activity.ReadRegistrySessionIDs(ctx, registryRoot)).To(ConsistOf(
				sessionID,
				"0bc9bb57-7034-49b5-b73c-70fe0682e953",
			))
		})

		It("is empty for a missing directory", func() {
			Expect(activity.ReadRegistrySessionIDs(ctx, filepath.Join(tmp, "nope"))).To(BeEmpty())
		})

		It("skips malformed and sessionless entries but keeps the valid ones", func() {
			registryRoot := filepath.Join(tmp, "sessions")
			Expect(os.MkdirAll(registryRoot, 0o750)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(registryRoot, "999.json"), []byte("{ this is not json"), 0o600)).To(Succeed())
			sessionless, err := json.Marshal(map[string]any{"pid": 1000, "cwd": "/tmp"})
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(registryRoot, "1000.json"), sessionless, 0o600)).To(Succeed())
			writeRegistryEntry(registryRoot, sessionID, 12345)

			Expect(activity.ReadRegistrySessionIDs(ctx, registryRoot)).To(ConsistOf(sessionID))
		})

		It("returns the default roots under the home directory", func() {
			Expect(activity.DefaultRegistryRoot()).To(HaveSuffix(filepath.Join(".claude", "sessions")))
			Expect(activity.DefaultProjectsRoot()).To(HaveSuffix(filepath.Join(".claude", "projects")))
		})
	})
})
