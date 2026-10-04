// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package watcher reproduces the Python vault_ui.vault_cli_watcher supervisor:
// one `vault-cli watch` subprocess covers every configured vault (a single
// comma-joined --vault value), each parsed JSON event is dispatched to a
// callback, and the subprocess is restarted after the restart delay on every
// exit while no stop was requested.
//
// This is a deliberate os/exec user: the watcher subprocess is one of the few
// places the port spawns an external process. Arguments are passed as argv,
// never through a shell.
package watcher

import (
	"bufio"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/golang/glog"
)

// typesArg is the fixed --types value the Python watcher passes: the four
// hierarchy kinds the board renders.
const typesArg = "task,goal,theme,objective"

// Defaults for the injected durations, carried by value.
const (
	// DefaultRestartDelay mirrors _RESTART_DELAY_SECONDS.
	DefaultRestartDelay = 5 * time.Second
	// DefaultStopTimeout mirrors _STOP_TIMEOUT_SECONDS.
	DefaultStopTimeout = 5 * time.Second
)

// Event is one parsed vault-cli watch event.
type Event struct {
	EventType string // the "event" field
	ItemID    string // the "name" field
	Vault     string // the "vault" field, defaulting to the first watched vault
	Kind      string // the "type" field (task/goal/theme/objective), or ""
}

// EventHandler receives every dispatched event.
type EventHandler func(Event)

// Supervisor runs one `vault-cli watch` subprocess for all vaults.
type Supervisor interface {
	// Run starts the subprocess and reads events until ctx is cancelled or Stop
	// is called. It restarts the subprocess after restartDelay on every exit
	// while no stop was requested.
	Run(ctx context.Context) error
	// Stop requests a stop and shuts the subprocess down with SIGTERM, escalating
	// to kill after stopTimeout.
	Stop(ctx context.Context) error
}

// CommandRunner spawns one subprocess. It exists as an injectable seam so a test
// can drive the restart/stop lifecycle without spawning a real vault-cli.
type CommandRunner func(ctx context.Context, name string, args ...string) *exec.Cmd

// Option customises a Supervisor.
type Option func(*supervisor)

// WithCommandRunner overrides how the watcher subprocess is spawned. Tests use
// it to inject scripted commands; production leaves it unset.
func WithCommandRunner(runner CommandRunner) Option {
	return func(s *supervisor) {
		s.runner = runner
	}
}

// NewSupervisor builds a supervisor for the given vaults. vaultNames are joined
// with commas into one --vault value; restartDelay and stopTimeout are injected.
func NewSupervisor(
	vaultCLIPath string,
	vaultNames []string,
	onEvent EventHandler,
	restartDelay, stopTimeout time.Duration,
	opts ...Option,
) Supervisor {
	s := &supervisor{
		vaultCLIPath: vaultCLIPath,
		vaultNames:   append([]string(nil), vaultNames...),
		onEvent:      onEvent,
		restartDelay: restartDelay,
		stopTimeout:  stopTimeout,
	}
	s.runner = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// #nosec G204 -- argv from configuration, never a shell
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		cmd.WaitDelay = stopTimeout
		return cmd
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type supervisor struct {
	vaultCLIPath string
	vaultNames   []string
	onEvent      EventHandler
	restartDelay time.Duration
	stopTimeout  time.Duration
	runner       CommandRunner

	mu      sync.Mutex
	proc    *subprocess
	stopped bool
}

// subprocess couples a spawned command with a broadcast signal for its exit.
// stdout is kept so a shutdown can close it and unblock the reader even when a
// grandchild still holds the pipe's write end open.
type subprocess struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	done   chan struct{}
}

func (s *supervisor) Run(ctx context.Context) error {
	s.mu.Lock()
	s.stopped = false
	s.mu.Unlock()

	for {
		if s.isStopped() {
			return nil
		}
		s.runSubprocess(ctx)
		if s.isStopped() {
			return nil
		}
		glog.Infof(
			"[VaultCLIWatcher] Restarting watcher for vaults %s in %ds",
			s.vaultsLabel(),
			int(s.restartDelay.Seconds()),
		)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(s.restartDelay):
		}
	}
}

func (s *supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.stopped = true
	proc := s.proc
	s.mu.Unlock()

	if proc == nil || proc.cmd.Process == nil {
		// Not started yet; runSubprocess honours the stop once it is.
		return nil
	}
	select {
	case <-proc.done:
		return nil
	default:
	}

	glog.Infof("[VaultCLIWatcher] Stopping watcher for vaults %s", s.vaultsLabel())
	_ = proc.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-proc.done:
		return nil
	case <-time.After(s.stopTimeout):
		glog.Warningf(
			"[VaultCLIWatcher] Process did not exit in time, killing vaults %s",
			s.vaultsLabel(),
		)
		_ = proc.cmd.Process.Kill()
		_ = proc.stdout.Close()
		<-proc.done
		return nil
	}
}

// runSubprocess runs one instance of the vault-cli watch subprocess for every
// vault, reads its events, and reaps it. A malformed line never propagates an
// error.
func (s *supervisor) runSubprocess(ctx context.Context) {
	glog.Infof("[VaultCLIWatcher] Starting vault-cli watch --vault %s", s.vaultsLabel())
	cmd := s.runner(
		ctx,
		s.vaultCLIPath,
		"watch",
		"--vault",
		s.vaultsLabel(),
		"--types",
		typesArg,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		glog.Errorf("[VaultCLIWatcher] Unexpected error for vaults %s: %v", s.vaultsLabel(), err)
		return
	}
	cmd.Stderr = nil

	// Register before Start so a concurrent Stop can always find the process
	// (or see cmd.Process == nil and defer to the stopped check below).
	proc := &subprocess{cmd: cmd, stdout: stdout, done: make(chan struct{})}
	s.mu.Lock()
	s.proc = proc
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.proc = nil
		s.mu.Unlock()
	}()

	if startErr := cmd.Start(); startErr != nil {
		glog.Errorf(
			"[VaultCLIWatcher] Unexpected error for vaults %s: %v",
			s.vaultsLabel(),
			startErr,
		)
		return
	}

	// A stop that raced this spawn saw no started process to signal, so honour
	// it here.
	if s.isStopped() {
		_ = proc.cmd.Process.Signal(syscall.SIGTERM)
	}

	// Close the pipe when the context is cancelled so a blocked read returns
	// even if a grandchild inherited the write end.
	stopWatch := context.AfterFunc(ctx, func() { _ = stdout.Close() })
	defer stopWatch()

	s.consume(ctx, stdout)
	code := s.finish(proc)
	close(proc.done)

	if !s.isStopped() && code != 0 && code != -int(syscall.SIGTERM) {
		glog.Errorf(
			"[VaultCLIWatcher] vault-cli exited with code %d for vaults %s",
			code,
			s.vaultsLabel(),
		)
	}
}

// consume reads and dispatches every line until EOF or ctx cancellation. It
// never returns an error: a malformed line is logged and skipped.
func (s *supervisor) consume(ctx context.Context, stdout io.Reader) {
	reader := bufio.NewReader(stdout)
	for {
		line, err := reader.ReadString('\n')
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			s.handleLine(trimmed)
		}
		if err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

// finish reaps the subprocess and returns its exit code. If it is still running
// it is sent SIGTERM and given stopTimeout to exit before being killed, so a
// wedged subprocess cannot linger. It is the only caller of cmd.Wait, so it
// never races the stdout reader.
func (s *supervisor) finish(proc *subprocess) int {
	_ = proc.cmd.Process.Signal(syscall.SIGTERM)
	timer := time.AfterFunc(s.stopTimeout, func() { _ = proc.cmd.Process.Kill() })
	defer timer.Stop()
	return exitCodeOf(proc.cmd.Wait())
}

// handleLine parses and dispatches a JSON event line. A line that is not valid
// JSON is logged and skipped; an event is dispatched only when both event and
// name are non-empty.
func (s *supervisor) handleLine(line string) {
	var event map[string]any
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		glog.Warningf("[VaultCLIWatcher] Failed to parse event line: %q", line)
		return
	}

	eventType, _ := event["event"].(string)
	itemID, _ := event["name"].(string)
	vault, ok := event["vault"].(string)
	if !ok {
		vault = s.defaultVault()
	}
	kind, _ := event["type"].(string)

	if eventType != "" && itemID != "" {
		glog.V(3).Infof(
			"[VaultCLIWatcher] Event %s: %s (vault: %s, kind: %s)",
			eventType,
			itemID,
			vault,
			kind,
		)
		s.onEvent(Event{EventType: eventType, ItemID: itemID, Vault: vault, Kind: kind})
	}
}

// defaultVault is the fallback for an event without a vault field.
func (s *supervisor) defaultVault() string {
	if len(s.vaultNames) == 0 {
		return ""
	}
	return s.vaultNames[0]
}

// vaultsLabel is the comma-joined vault names, for log messages and the --vault
// flag value.
func (s *supervisor) vaultsLabel() string {
	return strings.Join(s.vaultNames, ",")
}

func (s *supervisor) isStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

// exitCodeOf maps a cmd.Wait result to a Python-style returncode: 0 on success,
// the negated signal number when the process was signalled, the exit status
// otherwise.
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if stderrors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				return -int(status.Signal())
			}
			return status.ExitStatus()
		}
	}
	return -1
}
