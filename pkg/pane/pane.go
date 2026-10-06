// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pane resolves a live Claude session to its WezTerm pane and proxies a
// jump to that pane.
//
// A session's pane is resolved here, in Go: the harness session registry's
// current name for the session is matched against the WezTerm pane titles, so no
// Python interpreter and no helper script sit in the path.
//
// The board can tell that a session is live but cannot take the operator to it:
// the fleet-jump server that activates a pane requires a shared credential, and
// that credential must never reach the browser. So the jump is proxied here —
// the credential is read from its 0600 file, handed to the jump server, and
// never logged or returned to any caller that would expose it.
//
// Every function returns an expected absence (no token, no pane) rather than
// raising; only PerformJump returns an error, so its caller can map a
// jump-server failure to a status code.
package pane

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// Defaults carried by value, mirroring the Python module constants.
const (
	// DefaultResolveTimeout bounds one `wezterm cli list` call.
	DefaultResolveTimeout = 5 * time.Second
	// DefaultJumpTimeout mirrors _JUMP_TIMEOUT_SECONDS.
	DefaultJumpTimeout = 5 * time.Second
	// DefaultJumpServerURL mirrors _JUMP_SERVER_URL.
	DefaultJumpServerURL = "http://127.0.0.1:1337/jump"
	// DefaultWeztermBundleDir mirrors _WEZTERM_BUNDLE_DIR: the one known macOS
	// location of the wezterm executable.
	DefaultWeztermBundleDir = "/Applications/WezTerm.app/Contents/MacOS"
)

// Logger is this package's diagnostic sink.
type Logger interface {
	Debugf(format string, args ...any)
}

type glogLogger struct{}

func (glogLogger) Debugf(format string, args ...any) { glog.V(4).Infof(format, args...) }

var (
	loggerMu sync.RWMutex
	logger   Logger = glogLogger{}
)

// SetLogger installs the diagnostic sink used by this package, so a test can
// capture output and prove the jump token value never reaches a log line. A nil
// logger restores the default glog-backed sink.
func SetLogger(l Logger) {
	loggerMu.Lock()
	defer loggerMu.Unlock()
	if l == nil {
		logger = glogLogger{}
		return
	}
	logger = l
}

func logDebug(format string, args ...any) {
	loggerMu.RLock()
	l := logger
	loggerMu.RUnlock()
	l.Debugf(format, args...)
}

// JumpTokenPath is the path of the shared fleet-jump credential under homeDir.
func JumpTokenPath(homeDir string) string {
	return filepath.Join(homeDir, ".claude", "secrets", "jump-token")
}

// WeztermBinDir returns the WezTerm bundle dir when the wezterm binary exists in
// it, else ("", false). Only that one known location is checked.
func WeztermBinDir(bundleDir string) (string, bool) {
	if bundleDir == "" {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(bundleDir, "wezterm")); err != nil {
		return "", false
	}
	return bundleDir, true
}

// PidAlive reports whether pid is alive and signalable (a permission error and a
// non-positive pid both count as not-ours-to-use).
func PidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// guiSocketDir is the one directory scanned for WezTerm GUI sockets.
func guiSocketDir(homeDir string) string {
	return filepath.Join(homeDir, ".local", "share", "wezterm")
}

// WeztermGuiSocket returns the newest live `gui-sock-<pid>` under dir, or
// ("", false). A candidate counts only when the pid is alive and the suffix is
// ASCII digits.
func WeztermGuiSocket(dir string, pidAlive func(int) bool) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		logDebug("[PaneResolver] Cannot list %s: %v", dir, err)
		return "", false
	}

	best := ""
	var bestMtime time.Time
	for _, entry := range entries {
		name := entry.Name()
		suffix, ok := strings.CutPrefix(name, "gui-sock-")
		if !ok || !isASCIIDigits(suffix) {
			continue
		}
		pid, convErr := strconv.Atoi(suffix)
		if convErr != nil || pid <= 0 || !pidAlive(pid) {
			continue
		}
		info, statErr := os.Stat(filepath.Join(dir, name))
		if statErr != nil {
			continue
		}
		if best == "" || info.ModTime().After(bestMtime) {
			best = filepath.Join(dir, name)
			bestMtime = info.ModTime()
		}
	}
	if best == "" {
		return "", false
	}
	return best, true
}

// BuildSubprocessEnv returns a copy of env adjusted for the pane helper: the
// WezTerm bundle dir prepended to PATH when present, and WEZTERM_UNIX_SOCKET
// pointed at the newest live GUI socket when that variable is unset/empty. An
// explicitly set socket always wins. It NEVER mutates env.
func BuildSubprocessEnv(
	env []string,
	homeDir, bundleDir string,
	pidAlive func(int) bool,
) []string {
	out := append([]string(nil), env...)

	if binDir, ok := WeztermBinDir(bundleDir); ok {
		out = prependPath(out, binDir)
	} else {
		logDebug("[PaneResolver] wezterm not found in %s; pane resolution may fail", bundleDir)
	}

	if envValue(out, "WEZTERM_UNIX_SOCKET") == "" {
		if socket, ok := WeztermGuiSocket(guiSocketDir(homeDir), pidAlive); ok {
			out = setEnv(out, "WEZTERM_UNIX_SOCKET", socket)
		} else {
			logDebug(
				"[PaneResolver] no live WezTerm GUI socket found; " +
					"wezterm cli will use its default socket",
			)
		}
	}

	return out
}

// ReadJumpToken returns the stripped jump credential, or ("", false) when the
// file is missing, unreadable, or whitespace-only. The token VALUE is never
// logged and never returned to any caller that would expose it.
func ReadJumpToken(path string) (string, bool) {
	// #nosec G304 -- path from configuration, never request input
	content, err := os.ReadFile(path)
	if err != nil {
		logDebug("[PaneResolver] Cannot read jump token %s: %v", path, err)
		return "", false
	}
	token := strings.TrimSpace(string(content))
	if token == "" {
		return "", false
	}
	return token, true
}

// PerformJump asks the fleet-jump server to activate paneID. Both query values
// are percent-encoded. Returns an error for a non-2xx status or a transport
// failure.
func PerformJump(
	ctx context.Context,
	jumpServerURL, paneID, token string,
	timeout time.Duration,
) error {
	endpoint := jumpServerURL +
		"?pane=" + url.QueryEscape(paneID) +
		"&t=" + url.QueryEscape(token)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.Wrapf(ctx, err, "build jump request")
	}

	client := &http.Client{Timeout: timeout}
	response, err := client.Do(request)
	if err != nil {
		return errors.Wrapf(ctx, err, "jump request failed")
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.Errorf(ctx, "jump server returned HTTP %d", response.StatusCode)
	}
	return nil
}

// isASCIIDigits reports whether s is non-empty and made only of ASCII digits.
// Python's str.isdigit() accepts Unicode digits such as `²`, which int() then
// rejects, so the port filters them out here instead.
func isASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// envValue returns the value of the KEY= entry in env, or "" when absent.
func envValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, prefix); ok {
			return value
		}
	}
	return ""
}

// prependPath prepends dir to the PATH entry of env, or appends a PATH entry
// when none exists.
func prependPath(env []string, dir string) []string {
	for index, entry := range env {
		if value, ok := strings.CutPrefix(entry, "PATH="); ok {
			env[index] = "PATH=" + dir + string(os.PathListSeparator) + value
			return env
		}
	}
	return append(env, "PATH="+dir)
}

// setEnv replaces the KEY= entry in env, or appends it when absent.
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for index, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[index] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
