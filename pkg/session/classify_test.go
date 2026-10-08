// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session_test

import (
	"context"
	"os"
	"path/filepath"
	"time"

	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/activity"
	"github.com/bborbe/vault-ui/pkg/session"
)

const sessionID = "e0930886-0843-4ca9-adfa-58819443c032"

const freshID = "0bc9bb57-7034-49b5-b73c-70fe0682e953"

// baseTime is the fixed clock the classification tests run against; every
// transcript mtime and every injected Now is derived from it, so the suite has
// no wall-clock dependency.
var baseTime = time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)

func writeTranscript(directory, id string, age time.Duration) string {
	Expect(os.MkdirAll(directory, 0o750)).To(Succeed())
	path := filepath.Join(directory, id+".jsonl")
	Expect(os.WriteFile(path, []byte("{\"type\":\"mode\"}\n"), 0o600)).To(Succeed())
	when := baseTime.Add(-age)
	Expect(os.Chtimes(path, when, when)).To(Succeed())
	return path
}

func nowUTC() libtime.DateTime {
	return libtime.DateTime(baseTime).UTC()
}

var _ = Describe("ClassifySessionState", func() {
	var (
		ctx        context.Context
		tmp        string
		projects   string
		projectDir string
	)

	BeforeEach(func() {
		ctx = context.Background()
		tmp = GinkgoT().TempDir()
		projects = filepath.Join(tmp, "projects")
		projectDir = filepath.Join(projects, "-vault")
	})

	DescribeTable("classifies the session state",
		func(setup func() session.ClassifyParams, expected session.SessionState) {
			Expect(session.ClassifySessionState(ctx, setup())).To(Equal(expected))
		},
		Entry("empty-id-is-None-despite-registry",
			func() session.ClassifyParams {
				return session.ClassifyParams{
					SessionID:          "",
					ProjectDir:         projectDir,
					ProjectsRoot:       projects,
					Now:                nowUTC(),
					LiveWindow:         session.DefaultLiveWindow,
					RegistrySessionIDs: []string{sessionID},
				}
			},
			session.SessionStateNone,
		),
		Entry("stale-transcript-without-process-is-quiet",
			func() session.ClassifyParams {
				writeTranscript(projectDir, sessionID, 3*time.Hour)
				return session.ClassifyParams{
					SessionID:        sessionID,
					ProjectDir:       projectDir,
					ProjectsRoot:     projects,
					Now:              nowUTC(),
					LiveWindow:       session.DefaultLiveWindow,
					ResumeSessionIDs: []string{"some-other-session"},
				}
			},
			session.SessionStateQuiet,
		),
	)

	It("classifies a transcript written within the window as live", func() {
		writeTranscript(projectDir, sessionID, 30*time.Second)

		Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
			SessionID:    sessionID,
			ProjectDir:   projectDir,
			ProjectsRoot: projects,
			Now:          nowUTC(),
			LiveWindow:   session.DefaultLiveWindow,
		})).To(Equal(session.SessionStateLive))
	})

	It("classifies a transcript older than the window as quiet", func() {
		writeTranscript(projectDir, sessionID, 3*time.Hour)

		Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
			SessionID:        sessionID,
			ProjectDir:       projectDir,
			ProjectsRoot:     projects,
			Now:              nowUTC(),
			LiveWindow:       session.DefaultLiveWindow,
			ResumeSessionIDs: []string{},
		})).To(Equal(session.SessionStateQuiet))
	})

	It("treats a transcript exactly the window old as live, one second past as quiet", func() {
		path := writeTranscript(projectDir, sessionID, time.Hour)
		info, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		mtime := libtime.DateTime(info.ModTime()).UTC()

		Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
			SessionID:    sessionID,
			ProjectDir:   projectDir,
			ProjectsRoot: projects,
			Now:          mtime.Add(libtime.Duration(session.DefaultLiveWindow)),
			LiveWindow:   session.DefaultLiveWindow,
		})).To(Equal(session.SessionStateLive))

		Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
			SessionID:        sessionID,
			ProjectDir:       projectDir,
			ProjectsRoot:     projects,
			Now:              mtime.Add(libtime.Duration(session.DefaultLiveWindow + time.Second)),
			LiveWindow:       session.DefaultLiveWindow,
			ResumeSessionIDs: []string{},
		})).To(Equal(session.SessionStateQuiet))
	})

	Describe("the injected transcript probe", func() {
		probeReturning := func(value *libtime.DateTime) activity.TranscriptMtimeGetter {
			return func(_ context.Context, _, _, _ string) *libtime.DateTime { return value }
		}

		It("drives the live outcome without a real transcript file", func() {
			fresh := libtime.DateTime(baseTime.Add(-30 * time.Second)).UTC()

			Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
				SessionID:       sessionID,
				ProjectDir:      projectDir,
				ProjectsRoot:    projects,
				Now:             nowUTC(),
				LiveWindow:      session.DefaultLiveWindow,
				TranscriptMtime: probeReturning(fresh.Ptr()),
			})).To(Equal(session.SessionStateLive))
		})

		It("drives the quiet outcome without a real transcript file", func() {
			stale := libtime.DateTime(baseTime.Add(-3 * time.Hour)).UTC()

			Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
				SessionID:       sessionID,
				ProjectDir:      projectDir,
				ProjectsRoot:    projects,
				Now:             nowUTC(),
				LiveWindow:      session.DefaultLiveWindow,
				TranscriptMtime: probeReturning(stale.Ptr()),
			})).To(Equal(session.SessionStateQuiet))
		})

		It("beats the filesystem when it reports no transcript", func() {
			// A fresh transcript exists on disk; the injected probe says
			// otherwise, and the probe is authoritative.
			writeTranscript(projectDir, sessionID, 30*time.Second)

			Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
				SessionID:       sessionID,
				ProjectDir:      projectDir,
				ProjectsRoot:    projects,
				Now:             nowUTC(),
				LiveWindow:      session.DefaultLiveWindow,
				TranscriptMtime: probeReturning(nil),
			})).To(Equal(session.SessionStateIndeterminate))
		})

		It("keeps today's behaviour when no probe is injected", func() {
			writeTranscript(projectDir, sessionID, 30*time.Second)

			Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
				SessionID:    sessionID,
				ProjectDir:   projectDir,
				ProjectsRoot: projects,
				Now:          nowUTC(),
				LiveWindow:   session.DefaultLiveWindow,
			})).To(Equal(session.SessionStateLive))
		})
	})

	It("classifies a session with no transcript as indeterminate", func() {
		Expect(os.MkdirAll(projects, 0o750)).To(Succeed())

		Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
			SessionID:    sessionID,
			ProjectDir:   filepath.Join(projects, "-vault"),
			ProjectsRoot: projects,
			Now:          nowUTC(),
			LiveWindow:   session.DefaultLiveWindow,
		})).To(Equal(session.SessionStateIndeterminate))
	})

	It("lets the registry beat a stale transcript", func() {
		writeTranscript(projectDir, sessionID, 3*time.Hour)

		Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
			SessionID:          sessionID,
			ProjectDir:         projectDir,
			ProjectsRoot:       projects,
			Now:                nowUTC(),
			LiveWindow:         session.DefaultLiveWindow,
			ResumeSessionIDs:   []string{},
			RegistrySessionIDs: []string{sessionID},
		})).To(Equal(session.SessionStateLive))
	})

	It("reads registry-live without a transcript as live, not indeterminate", func() {
		Expect(os.MkdirAll(projects, 0o750)).To(Succeed())

		Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
			SessionID:          sessionID,
			ProjectDir:         filepath.Join(projects, "-vault"),
			ProjectsRoot:       projects,
			Now:                nowUTC(),
			LiveWindow:         session.DefaultLiveWindow,
			RegistrySessionIDs: []string{sessionID},
		})).To(Equal(session.SessionStateLive))
	})

	It("keeps an unregistered stale transcript quiet", func() {
		writeTranscript(projectDir, sessionID, 3*time.Hour)

		Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
			SessionID:          sessionID,
			ProjectDir:         projectDir,
			ProjectsRoot:       projects,
			Now:                nowUTC(),
			LiveWindow:         session.DefaultLiveWindow,
			ResumeSessionIDs:   []string{},
			RegistrySessionIDs: []string{"some-other-session"},
		})).To(Equal(session.SessionStateQuiet))
	})

	It("reads an unknown live-id source with a stale transcript as indeterminate", func() {
		writeTranscript(projectDir, sessionID, 3*time.Hour)

		Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
			SessionID:          sessionID,
			ProjectDir:         projectDir,
			ProjectsRoot:       projects,
			Now:                nowUTC(),
			LiveWindow:         session.DefaultLiveWindow,
			ResumeSessionIDs:   []string{},
			RegistrySessionIDs: []string{},
			LiveIDsUnknown:     true,
		})).To(Equal(session.SessionStateIndeterminate))
	})

	It("keeps a fresh transcript live even when the live-id source is unknown", func() {
		writeTranscript(projectDir, sessionID, 30*time.Second)

		Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
			SessionID:          sessionID,
			ProjectDir:         projectDir,
			ProjectsRoot:       projects,
			Now:                nowUTC(),
			LiveWindow:         session.DefaultLiveWindow,
			ResumeSessionIDs:   []string{},
			RegistrySessionIDs: []string{},
			LiveIDsUnknown:     true,
		})).To(Equal(session.SessionStateLive))
	})

	It("still reads a listed session live when the source is unknown", func() {
		writeTranscript(projectDir, sessionID, 3*time.Hour)

		Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
			SessionID:          sessionID,
			ProjectDir:         projectDir,
			ProjectsRoot:       projects,
			Now:                nowUTC(),
			LiveWindow:         session.DefaultLiveWindow,
			ResumeSessionIDs:   []string{},
			RegistrySessionIDs: []string{sessionID},
			LiveIDsUnknown:     true,
		})).To(Equal(session.SessionStateLive))
	})

	It("matches the transcript/ps model when the registry is absent", func() {
		writeTranscript(projectDir, sessionID, 3*time.Hour)
		writeTranscript(projectDir, freshID, 30*time.Second)

		base := session.ClassifyParams{
			ProjectDir:         projectDir,
			ProjectsRoot:       projects,
			Now:                nowUTC(),
			LiveWindow:         session.DefaultLiveWindow,
			ResumeSessionIDs:   []string{},
			RegistrySessionIDs: []string{},
		}
		stale := base
		stale.SessionID = sessionID
		fresh := base
		fresh.SessionID = freshID

		Expect(session.ClassifySessionState(ctx, stale)).To(Equal(session.SessionStateQuiet))
		Expect(session.ClassifySessionState(ctx, fresh)).To(Equal(session.SessionStateLive))
	})

	It("keeps an open-but-idle session with a live resume process live", func() {
		writeTranscript(projectDir, sessionID, 3*time.Hour)

		Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
			SessionID:        sessionID,
			ProjectDir:       projectDir,
			ProjectsRoot:     projects,
			Now:              nowUTC(),
			LiveWindow:       session.DefaultLiveWindow,
			ResumeSessionIDs: []string{sessionID},
		})).To(Equal(session.SessionStateLive))
	})

	It("keeps a headless --session-id process live", func() {
		writeTranscript(projectDir, freshID, 3*time.Hour)
		liveIDs := session.ParseLiveSessionIDs(psHeadless)

		Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
			SessionID:        freshID,
			ProjectDir:       projectDir,
			ProjectsRoot:     projects,
			Now:              nowUTC(),
			LiveWindow:       session.DefaultLiveWindow,
			ResumeSessionIDs: liveIDs,
		})).To(Equal(session.SessionStateLive))
	})

	It("does not treat a fresh task-file mtime as liveness", func() {
		Expect(os.MkdirAll(projects, 0o750)).To(Succeed())

		Expect(session.ClassifySessionState(ctx, session.ClassifyParams{
			SessionID:    sessionID,
			ProjectDir:   filepath.Join(projects, "-vault"),
			ProjectsRoot: projects,
			Now:          nowUTC(),
			LiveWindow:   session.DefaultLiveWindow,
		})).To(Equal(session.SessionStateIndeterminate))
	})
})
