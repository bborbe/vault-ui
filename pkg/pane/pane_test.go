// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pane_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/pane"
)

// captureLogger records every log line so a test can prove a value never
// reaches it.
type captureLogger struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *captureLogger) Debugf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(&c.buf, format+"\n", args...)
}

func (c *captureLogger) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// writeScript writes an executable shell script and returns its path.
func writeScript(dir, body string) string {
	path := filepath.Join(dir, "helper.sh")
	ExpectWithOffset(1, os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700)).To(Succeed())
	return path
}

// makeSocket creates ~/.local/share/wezterm/gui-sock-<pid> under home with the
// given mtime. It is a regular file, not a socket: discovery never checks type.
func makeSocket(home string, pid int, mtime time.Time) string {
	dir := filepath.Join(home, ".local", "share", "wezterm")
	ExpectWithOffset(1, os.MkdirAll(dir, 0o750)).To(Succeed())
	path := filepath.Join(dir, fmt.Sprintf("gui-sock-%d", pid))
	ExpectWithOffset(1, os.WriteFile(path, nil, 0o600)).To(Succeed())
	ExpectWithOffset(1, os.Chtimes(path, mtime, mtime)).To(Succeed())
	return path
}

// aliveOnly is a pidAlive stand-in reporting only the given pids as live.
func aliveOnly(pids ...int) func(int) bool {
	return func(pid int) bool {
		for _, alive := range pids {
			if pid == alive {
				return true
			}
		}
		return false
	}
}

// envValueOf returns the value of the KEY= entry in env, or "" when absent.
func envValueOf(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, prefix); ok {
			return value
		}
	}
	return ""
}

var _ = Describe("PaneResolver", func() {
	DescribeTable("PaneResolver",
		func(body func(dir string)) {
			body(GinkgoT().TempDir())
		},
		Entry("timeout-kills-helper", func(dir string) {
			pidFile := filepath.Join(dir, "pid")
			// exec replaces the shell with sleep, so no grandchild holds the
			// stdout pipe open after the helper is killed.
			scriptPath := writeScript(dir, "echo $$ > "+pidFile+"\nexec sleep 30")

			paneID, ok := pane.ResolvePaneID(
				context.Background(),
				"/bin/sh",
				scriptPath,
				"e0930886-0843-4ca9-adfa-58819443c032",
				os.Environ(),
				100*time.Millisecond,
			)

			Expect(ok).To(BeFalse())
			Expect(paneID).To(Equal(""))

			pidBytes, err := os.ReadFile(pidFile)
			Expect(err).NotTo(HaveOccurred())
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
			Expect(convErr).NotTo(HaveOccurred())
			// The killed helper must not outlive the call.
			Eventually(func() bool { return pane.PidAlive(pid) }).Should(BeFalse())
		}),
		Entry("jump-percent-encodes-both-query-values", func(dir string) {
			queries := make(chan url.Values, 1)
			server := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					queries <- r.URL.Query()
					w.WriteHeader(http.StatusOK)
				},
			))
			DeferCleanup(server.Close)

			paneID := "w1:p7"
			token := "a&b=c d/e?f"
			Expect(pane.PerformJump(
				context.Background(),
				server.URL,
				paneID,
				token,
				2*time.Second,
			)).To(Succeed())

			var query url.Values
			Eventually(queries).Should(Receive(&query))
			Expect(query.Get("pane")).To(Equal(paneID))
			Expect(query.Get("t")).To(Equal(token))
		}),
	)

	It("returns the stripped jump token value", func() {
		path := filepath.Join(GinkgoT().TempDir(), "jump-token")
		Expect(os.WriteFile(path, []byte("  s3cr3t-token\n"), 0o600)).To(Succeed())

		token, ok := pane.ReadJumpToken(path)
		Expect(ok).To(BeTrue())
		Expect(token).To(Equal("s3cr3t-token"))
	})

	It("returns false for a missing token path", func() {
		_, ok := pane.ReadJumpToken(filepath.Join(GinkgoT().TempDir(), "missing"))
		Expect(ok).To(BeFalse())
	})

	It("returns false for a whitespace-only token file", func() {
		path := filepath.Join(GinkgoT().TempDir(), "jump-token")
		Expect(os.WriteFile(path, []byte("   \n\t\n"), 0o600)).To(Succeed())

		_, ok := pane.ReadJumpToken(path)
		Expect(ok).To(BeFalse())
	})

	It("never logs the jump token value on either path", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "jump-token")
		Expect(os.WriteFile(path, []byte("super-secret-value\n"), 0o600)).To(Succeed())

		captured := &captureLogger{}
		pane.SetLogger(captured)
		DeferCleanup(func() { pane.SetLogger(nil) })

		token, ok := pane.ReadJumpToken(path)
		Expect(ok).To(BeTrue())
		Expect(token).To(Equal("super-secret-value"))

		_, ok = pane.ReadJumpToken(filepath.Join(dir, "missing"))
		Expect(ok).To(BeFalse())

		Expect(captured.String()).NotTo(ContainSubstring("super-secret-value"))
	})

	It("builds the jump token path under home secrets", func() {
		Expect(pane.JumpTokenPath("/home/operator")).To(
			Equal(filepath.Join("/home/operator", ".claude", "secrets", "jump-token")),
		)
	})

	It("prefers the plugin root for the pane-resolution script", func() {
		Expect(pane.WhoNeedsMePath("/opt/supervisor", "/home/operator")).To(
			Equal(filepath.Join("/opt/supervisor", "scripts", "who-needs-me.py")),
		)
	})

	It("falls back to the default marketplace location", func() {
		Expect(pane.WhoNeedsMePath("", "/home/operator")).To(
			Equal(filepath.Join(
				"/home/operator",
				".claude",
				"plugins",
				"marketplaces",
				"claude-supervisor",
				"scripts",
				"who-needs-me.py",
			)),
		)
	})

	It("returns the bundle dir only when wezterm exists inside it", func() {
		present := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(present, "wezterm"), nil, 0o600)).To(Succeed())
		dir, ok := pane.WeztermBinDir(present)
		Expect(ok).To(BeTrue())
		Expect(dir).To(Equal(present))

		absent := GinkgoT().TempDir()
		_, ok = pane.WeztermBinDir(absent)
		Expect(ok).To(BeFalse())
	})

	It("reports the current process as alive and non-positive pids as dead", func() {
		Expect(pane.PidAlive(os.Getpid())).To(BeTrue())
		Expect(pane.PidAlive(0)).To(BeFalse())
		Expect(pane.PidAlive(-1)).To(BeFalse())
	})

	It("picks the newest live GUI socket", func() {
		home := GinkgoT().TempDir()
		makeSocket(home, 111, time.Unix(1000, 0))
		newest := makeSocket(home, 222, time.Unix(3000, 0))
		makeSocket(home, 333, time.Unix(2000, 0))

		socket, ok := pane.WeztermGuiSocket(
			filepath.Join(home, ".local", "share", "wezterm"),
			aliveOnly(111, 222, 333),
		)
		Expect(ok).To(BeTrue())
		Expect(socket).To(Equal(newest))
	})

	It("skips a socket whose pid is dead", func() {
		home := GinkgoT().TempDir()
		live := makeSocket(home, 111, time.Unix(1000, 0))
		makeSocket(home, 222, time.Unix(3000, 0))

		socket, ok := pane.WeztermGuiSocket(
			filepath.Join(home, ".local", "share", "wezterm"),
			aliveOnly(111),
		)
		Expect(ok).To(BeTrue())
		Expect(socket).To(Equal(live))
	})

	It("ignores non-matching socket names", func() {
		home := GinkgoT().TempDir()
		dir := filepath.Join(home, ".local", "share", "wezterm")
		Expect(os.MkdirAll(dir, 0o750)).To(Succeed())
		for _, name := range []string{"sock", "gui-sock-abc", "gui-sock-"} {
			Expect(os.WriteFile(filepath.Join(dir, name), nil, 0o600)).To(Succeed())
		}

		_, ok := pane.WeztermGuiSocket(dir, aliveOnly(111))
		Expect(ok).To(BeFalse())
	})

	It("skips a socket whose suffix is not ASCII digits", func() {
		home := GinkgoT().TempDir()
		dir := filepath.Join(home, ".local", "share", "wezterm")
		Expect(os.MkdirAll(dir, 0o750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "gui-sock-²"), nil, 0o600)).To(Succeed())

		_, ok := pane.WeztermGuiSocket(dir, func(int) bool { return true })
		Expect(ok).To(BeFalse())
	})

	It("prepends the WezTerm bundle and never mutates the input env", func() {
		home := GinkgoT().TempDir()
		bundle := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(bundle, "wezterm"), nil, 0o600)).To(Succeed())

		env := []string{"PATH=/usr/bin:/bin", "HOME=/home/operator"}
		snapshot := append([]string(nil), env...)

		out := pane.BuildSubprocessEnv(env, home, bundle, aliveOnly())

		Expect(envValueOf(out, "PATH")).To(Equal(
			bundle + string(os.PathListSeparator) + "/usr/bin:/bin",
		))
		Expect(env).To(Equal(snapshot))
	})

	It("points WEZTERM_UNIX_SOCKET at the newest live GUI socket", func() {
		home := GinkgoT().TempDir()
		newest := makeSocket(home, 222, time.Unix(3000, 0))
		makeSocket(home, 111, time.Unix(1000, 0))

		out := pane.BuildSubprocessEnv(
			[]string{"PATH=/usr/bin"},
			home,
			filepath.Join(home, "no-bundle"),
			aliveOnly(111, 222),
		)

		Expect(envValueOf(out, "WEZTERM_UNIX_SOCKET")).To(Equal(newest))
	})

	It("preserves an explicitly set WEZTERM_UNIX_SOCKET", func() {
		home := GinkgoT().TempDir()
		makeSocket(home, 222, time.Unix(3000, 0))

		out := pane.BuildSubprocessEnv(
			[]string{"PATH=/usr/bin", "WEZTERM_UNIX_SOCKET=/explicit/sock"},
			home,
			filepath.Join(home, "no-bundle"),
			aliveOnly(222),
		)

		Expect(envValueOf(out, "WEZTERM_UNIX_SOCKET")).To(Equal("/explicit/sock"))
	})

	It("passes the exact argv with only the first 8 characters of the session id", func() {
		dir := GinkgoT().TempDir()
		argsFile := filepath.Join(dir, "args")
		recorder := writeScript(dir, `printf '%s\n' "$@" > `+argsFile+"\nprintf '42\\n'")
		scriptPath := "/opt/supervisor/scripts/who-needs-me.py"

		paneID, ok := pane.ResolvePaneID(
			context.Background(),
			recorder,
			scriptPath,
			"e0930886-0843-4ca9-adfa-58819443c032",
			os.Environ(),
			time.Second,
		)
		Expect(ok).To(BeTrue())
		Expect(paneID).To(Equal("42"))

		argsBytes, err := os.ReadFile(argsFile)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Split(strings.TrimSpace(string(argsBytes)), "\n")).To(Equal([]string{
			scriptPath,
			"--pane-for",
			"e0930886",
		}))
	})

	It("never spawns for an empty session id", func() {
		dir := GinkgoT().TempDir()
		spawned := filepath.Join(dir, "spawned")
		recorder := writeScript(dir, "touch "+spawned+"\nprintf '42\\n'")

		paneID, ok := pane.ResolvePaneID(
			context.Background(),
			recorder,
			"/opt/supervisor/scripts/who-needs-me.py",
			"",
			os.Environ(),
			time.Second,
		)
		Expect(ok).To(BeFalse())
		Expect(paneID).To(Equal(""))

		_, err := os.Stat(spawned)
		Expect(err).To(HaveOccurred())
	})

	It("returns false for empty stdout and for a non-zero exit", func() {
		emptyOut, ok := pane.ResolvePaneID(
			context.Background(),
			"/bin/true",
			"/opt/supervisor/scripts/who-needs-me.py",
			"e0930886-0843-4ca9-adfa-58819443c032",
			os.Environ(),
			time.Second,
		)
		Expect(ok).To(BeFalse())
		Expect(emptyOut).To(Equal(""))

		nonZero, ok := pane.ResolvePaneID(
			context.Background(),
			"/bin/false",
			"/opt/supervisor/scripts/who-needs-me.py",
			"e0930886-0843-4ca9-adfa-58819443c032",
			os.Environ(),
			time.Second,
		)
		Expect(ok).To(BeFalse())
		Expect(nonZero).To(Equal(""))
	})

	It("returns an error on a non-2xx jump status", func() {
		server := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
		))
		DeferCleanup(server.Close)

		err := pane.PerformJump(context.Background(), server.URL, "42", "token", time.Second)
		Expect(err).To(HaveOccurred())
	})
})
