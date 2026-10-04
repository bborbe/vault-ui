// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package watcher_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/watcher"
)

// recorder captures the argv of every spawn request the supervisor makes.
type recorder struct {
	mu    sync.Mutex
	calls [][]string
}

func (r *recorder) add(name string, args []string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string{name}, args...))
	return len(r.calls) - 1
}

func (r *recorder) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *recorder) at(index int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[index]
}

// runnerFor records each spawn request and delegates the command to build.
func runnerFor(r *recorder, build func(ctx context.Context, index int) *exec.Cmd) watcher.CommandRunner {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		index := r.add(name, args)
		return build(ctx, index)
	}
}

// writeScript writes an executable shell script and returns its path.
func writeScript(dir, body string) string {
	path := filepath.Join(dir, "script.sh")
	ExpectWithOffset(1, os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700)).To(Succeed())
	return path
}

// startRun runs the supervisor in the background and returns its result channel.
func startRun(s watcher.Supervisor, ctx context.Context) chan error {
	errCh := make(chan error, 1)
	go func() { errCh <- s.Run(ctx) }()
	return errCh
}

// eventSink is a thread-safe event collector.
type eventSink struct {
	mu     sync.Mutex
	events []watcher.Event
}

func (s *eventSink) handler(event watcher.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *eventSink) all() []watcher.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]watcher.Event(nil), s.events...)
}

var _ = Describe("Supervisor", func() {
	DescribeTable("Supervisor",
		func(body func(dir string)) {
			body(GinkgoT().TempDir())
		},
		Entry("restart-after-nonzero-exit", func(dir string) {
			sink := &eventSink{}
			rec := &recorder{}
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)

			s := watcher.NewSupervisor(
				"vault-cli",
				[]string{"alpha"},
				sink.handler,
				10*time.Millisecond,
				10*time.Second,
				watcher.WithCommandRunner(runnerFor(rec, func(ctx context.Context, index int) *exec.Cmd {
					if index == 0 {
						return exec.CommandContext(ctx, "sh", "-c", "exit 1")
					}
					return exec.CommandContext(ctx, "sleep", "30")
				})),
			)

			errCh := startRun(s, ctx)
			Eventually(rec.len).Should(BeNumerically(">=", 2))
			Expect(s.Stop(context.Background())).To(Succeed())
			Eventually(errCh).Should(Receive(BeNil()))
		}),
		Entry("malformed-json-line-is-skipped", func(dir string) {
			sink := &eventSink{}
			rec := &recorder{}
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)

			scriptPath := writeScript(dir,
				`printf 'not json\n{"event":"created","name":"T","vault":"V","type":"task"}\n'
sleep 30`)

			s := watcher.NewSupervisor(
				"vault-cli",
				[]string{"alpha"},
				sink.handler,
				10*time.Millisecond,
				10*time.Second,
				watcher.WithCommandRunner(runnerFor(rec, func(ctx context.Context, index int) *exec.Cmd {
					return exec.CommandContext(ctx, "sh", scriptPath)
				})),
			)

			errCh := startRun(s, ctx)
			Eventually(func() []watcher.Event { return sink.all() }).Should(
				Equal([]watcher.Event{
					{EventType: "created", ItemID: "T", Vault: "V", Kind: "task"},
				}),
			)
			cancel()
			Eventually(errCh).Should(Receive(BeNil()))
		}),
	)

	It("dispatches a valid event with every field", func() {
		dir := GinkgoT().TempDir()
		sink := &eventSink{}
		rec := &recorder{}
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		scriptPath := writeScript(dir,
			`printf '{"event":"modified","name":"My Task","vault":"TestVault","type":"goal"}\n'
sleep 30`)

		s := watcher.NewSupervisor(
			"vault-cli",
			[]string{"TestVault"},
			sink.handler,
			10*time.Millisecond,
			10*time.Second,
			watcher.WithCommandRunner(runnerFor(rec, func(ctx context.Context, index int) *exec.Cmd {
				return exec.CommandContext(ctx, "sh", scriptPath)
			})),
		)

		errCh := startRun(s, ctx)
		Eventually(func() []watcher.Event { return sink.all() }).Should(
			Equal([]watcher.Event{
				{EventType: "modified", ItemID: "My Task", Vault: "TestVault", Kind: "goal"},
			}),
		)
		cancel()
		Eventually(errCh).Should(Receive(BeNil()))
	})

	It("skips empty lines but dispatches the next event", func() {
		dir := GinkgoT().TempDir()
		sink := &eventSink{}
		rec := &recorder{}
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		scriptPath := writeScript(dir,
			`printf '\n\n{"event":"deleted","name":"Task","vault":"V","type":"task"}\n'
sleep 30`)

		s := watcher.NewSupervisor(
			"vault-cli",
			[]string{"V"},
			sink.handler,
			10*time.Millisecond,
			10*time.Second,
			watcher.WithCommandRunner(runnerFor(rec, func(ctx context.Context, index int) *exec.Cmd {
				return exec.CommandContext(ctx, "sh", scriptPath)
			})),
		)

		errCh := startRun(s, ctx)
		Eventually(func() []watcher.Event { return sink.all() }).Should(
			Equal([]watcher.Event{
				{EventType: "deleted", ItemID: "Task", Vault: "V", Kind: "task"},
			}),
		)
		cancel()
		Eventually(errCh).Should(Receive(BeNil()))
	})

	It("skips an event with an empty name", func() {
		dir := GinkgoT().TempDir()
		sink := &eventSink{}
		rec := &recorder{}
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		scriptPath := writeScript(dir,
			`printf '{"event":"modified","name":"","vault":"V","type":"task"}\n'
sleep 30`)

		s := watcher.NewSupervisor(
			"vault-cli",
			[]string{"V"},
			sink.handler,
			10*time.Millisecond,
			10*time.Second,
			watcher.WithCommandRunner(runnerFor(rec, func(ctx context.Context, index int) *exec.Cmd {
				return exec.CommandContext(ctx, "sh", scriptPath)
			})),
		)

		errCh := startRun(s, ctx)
		Eventually(rec.len).Should(Equal(1))
		Consistently(func() []watcher.Event { return sink.all() }, 200*time.Millisecond).Should(BeEmpty())
		cancel()
		Eventually(errCh).Should(Receive(BeNil()))
	})

	It("defaults the vault to the first watched vault", func() {
		dir := GinkgoT().TempDir()
		sink := &eventSink{}
		rec := &recorder{}
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		scriptPath := writeScript(dir,
			`printf '{"event":"modified","name":"My Task","type":"goal"}\n'
sleep 30`)

		s := watcher.NewSupervisor(
			"vault-cli",
			[]string{"first", "second"},
			sink.handler,
			10*time.Millisecond,
			10*time.Second,
			watcher.WithCommandRunner(runnerFor(rec, func(ctx context.Context, index int) *exec.Cmd {
				return exec.CommandContext(ctx, "sh", scriptPath)
			})),
		)

		errCh := startRun(s, ctx)
		Eventually(func() []watcher.Event { return sink.all() }).Should(
			Equal([]watcher.Event{
				{EventType: "modified", ItemID: "My Task", Vault: "first", Kind: "goal"},
			}),
		)
		cancel()
		Eventually(errCh).Should(Receive(BeNil()))
	})

	It("passes an empty kind when the type field is missing", func() {
		dir := GinkgoT().TempDir()
		sink := &eventSink{}
		rec := &recorder{}
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		scriptPath := writeScript(dir,
			`printf '{"event":"modified","name":"X","vault":"V"}\n'
sleep 30`)

		s := watcher.NewSupervisor(
			"vault-cli",
			[]string{"V"},
			sink.handler,
			10*time.Millisecond,
			10*time.Second,
			watcher.WithCommandRunner(runnerFor(rec, func(ctx context.Context, index int) *exec.Cmd {
				return exec.CommandContext(ctx, "sh", scriptPath)
			})),
		)

		errCh := startRun(s, ctx)
		Eventually(func() []watcher.Event { return sink.all() }).Should(
			Equal([]watcher.Event{
				{EventType: "modified", ItemID: "X", Vault: "V", Kind: ""},
			}),
		)
		cancel()
		Eventually(errCh).Should(Receive(BeNil()))
	})

	It("spawns one subprocess with one comma-joined --vault for all vaults", func() {
		sink := &eventSink{}
		rec := &recorder{}
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		s := watcher.NewSupervisor(
			"vault-cli",
			[]string{"alpha", "beta", "gamma"},
			sink.handler,
			10*time.Millisecond,
			10*time.Second,
			watcher.WithCommandRunner(runnerFor(rec, func(ctx context.Context, index int) *exec.Cmd {
				return exec.CommandContext(ctx, "sleep", "30")
			})),
		)

		errCh := startRun(s, ctx)
		Eventually(rec.len).Should(Equal(1))
		Expect(rec.at(0)).To(Equal([]string{
			"vault-cli",
			"watch",
			"--vault",
			"alpha,beta,gamma",
			"--types",
			"task,goal,theme,objective",
		}))
		Expect(countFlag(rec.at(0), "--vault")).To(Equal(1))
		cancel()
		Eventually(errCh).Should(Receive(BeNil()))
	})

	It("uses one --vault flag for a single vault", func() {
		sink := &eventSink{}
		rec := &recorder{}
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		s := watcher.NewSupervisor(
			"vault-cli",
			[]string{"solo"},
			sink.handler,
			10*time.Millisecond,
			10*time.Second,
			watcher.WithCommandRunner(runnerFor(rec, func(ctx context.Context, index int) *exec.Cmd {
				return exec.CommandContext(ctx, "sleep", "30")
			})),
		)

		errCh := startRun(s, ctx)
		Eventually(rec.len).Should(Equal(1))
		Expect(rec.at(0)).To(Equal([]string{
			"vault-cli",
			"watch",
			"--vault",
			"solo",
			"--types",
			"task,goal,theme,objective",
		}))
		Expect(countFlag(rec.at(0), "--vault")).To(Equal(1))
		cancel()
		Eventually(errCh).Should(Receive(BeNil()))
	})

	It("sends SIGTERM on Stop and waits for the subprocess to exit", func() {
		dir := GinkgoT().TempDir()
		sink := &eventSink{}
		rec := &recorder{}
		ready := filepath.Join(dir, "ready-flag")
		flag := filepath.Join(dir, "term-flag")
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		scriptPath := writeScript(dir,
			// Arm the trap before signalling readiness, so `ready` means
			// "safe to signal". Reversed, a SIGTERM landing in the touch->trap
			// window kills the shell by default action and the trap never runs.
			"trap 'touch "+flag+"; exit 0' TERM\ntouch "+ready+"\nwhile :; do sleep 0.05; done")

		s := watcher.NewSupervisor(
			"vault-cli",
			[]string{"alpha"},
			sink.handler,
			10*time.Millisecond,
			10*time.Second,
			watcher.WithCommandRunner(runnerFor(rec, func(ctx context.Context, index int) *exec.Cmd {
				return exec.CommandContext(ctx, "sh", scriptPath)
			})),
		)

		errCh := startRun(s, ctx)
		Eventually(rec.len).Should(Equal(1))
		// Wait until the subprocess is actually running, so Stop has a started
		// process to signal.
		Eventually(func() error {
			_, readyErr := os.Stat(ready)
			return readyErr
		}).Should(Succeed())

		Expect(s.Stop(context.Background())).To(Succeed())
		// Stop waits for the process to exit, and the trap touches the flag
		// before exiting, so the flag must already exist when Stop returns.
		_, statErr := os.Stat(flag)
		Expect(statErr).NotTo(HaveOccurred())

		cancel()
		Eventually(errCh).Should(Receive(BeNil()))
	})

	It("suppresses the restart when stopped during the restart window", func() {
		sink := &eventSink{}
		rec := &recorder{}
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		s := watcher.NewSupervisor(
			"vault-cli",
			[]string{"alpha"},
			sink.handler,
			500*time.Millisecond,
			10*time.Second,
			watcher.WithCommandRunner(runnerFor(rec, func(ctx context.Context, index int) *exec.Cmd {
				return exec.CommandContext(ctx, "sh", "-c", "exit 0")
			})),
		)

		errCh := startRun(s, ctx)
		Eventually(rec.len).Should(Equal(1))
		Consistently(rec.len, 150*time.Millisecond).Should(Equal(1))

		Expect(s.Stop(context.Background())).To(Succeed())
		Consistently(rec.len, 300*time.Millisecond).Should(Equal(1))

		cancel()
		Eventually(errCh).Should(Receive(BeNil()))
	})

	It("exits cleanly on context cancellation", func() {
		sink := &eventSink{}
		rec := &recorder{}
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		s := watcher.NewSupervisor(
			"vault-cli",
			[]string{"alpha"},
			sink.handler,
			10*time.Millisecond,
			10*time.Second,
			watcher.WithCommandRunner(runnerFor(rec, func(ctx context.Context, index int) *exec.Cmd {
				return exec.CommandContext(ctx, "sleep", "30")
			})),
		)

		errCh := startRun(s, ctx)
		Eventually(rec.len).Should(Equal(1))

		cancel()
		Eventually(errCh).Should(Receive(BeNil()))
	})
})

// countFlag returns how often flag appears in argv.
func countFlag(argv []string, flag string) int {
	count := 0
	for _, arg := range argv {
		if arg == flag {
			count++
		}
	}
	return count
}
