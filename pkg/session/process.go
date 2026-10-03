// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package session

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/bborbe/errors"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"
)

const uuidPattern = `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`

// sessionIDFlagRe matches either session-pinning flag followed by an exact uuid:
// `--resume <uuid>` (interactive resume) or `--session-id <uuid>` (headless launch).
var sessionIDFlagRe = regexp.MustCompile(`(?i)(?:--resume|--session-id)\s+(` + uuidPattern + `)`)

// launchIDFlagRe matches ONLY the `--session-id <uuid>` launch pin.
var launchIDFlagRe = regexp.MustCompile(`(?i)--session-id\s+(` + uuidPattern + `)`)

// ProcessScanner returns fresh `ps` output. The production scanner runs ps;
// tests inject a fixed string.
type ProcessScanner func(ctx context.Context) (string, error)

// NewPSScanner returns a ProcessScanner that runs `ps` with the given args
// (production read paths use `-axww`, `-o`, `args=`).
func NewPSScanner(args ...string) ProcessScanner {
	return func(ctx context.Context) (string, error) {
		cmd := exec.CommandContext(ctx, "ps", args...)
		out, err := cmd.Output()
		if err != nil {
			return "", errors.Wrap(ctx, err, "run ps")
		}
		return string(out), nil
	}
}

// ParseLiveSessionIDs returns the session ids of live claude processes from a
// `ps` table. An exact `--resume <uuid>` or `--session-id <uuid>` match only,
// and only on a row containing the `claude` token.
func ParseLiveSessionIDs(psOutput string) []string {
	ids := []string{}
	for _, line := range strings.Split(psOutput, "\n") {
		if !strings.Contains(line, "claude") {
			continue
		}
		if match := sessionIDFlagRe.FindStringSubmatch(line); match != nil {
			ids = append(ids, match[1])
		}
	}
	return ids
}

// ParseLiveSessionNames maps `-n <name>` -> `--session-id <uuid>` for live claude
// processes. A name bound to two different uuids in one scan is ambiguous and
// omitted.
func ParseLiveSessionNames(psOutput string) map[string]string {
	return parseNames(psOutput, sessionIDFlagRe)
}

// ParseLiveProcesses maps session id -> PID of live claude processes from a
// `ps -o pid=,args=` table (the first whitespace field is the PID). A
// non-numeric PID prefix is skipped, not fatal.
func ParseLiveProcesses(psOutput string) map[string]int {
	return parseProcesses(psOutput, sessionIDFlagRe)
}

// ParseLaunchProcesses maps session id -> PID for launch rows only
// (`--session-id`); a `--resume` row is an interactive resume, never a launch.
func ParseLaunchProcesses(psOutput string) map[string]int {
	return parseProcesses(psOutput, launchIDFlagRe)
}

// ParseLaunchNames maps `-n <name>` -> session id for launch rows only
// (`--session-id`).
func ParseLaunchNames(psOutput string) map[string]string {
	return parseNames(psOutput, launchIDFlagRe)
}

// ProcessTable is the raw-ps cache (one TTL for the whole board, not per card),
// from which both derived views are parsed.
type ProcessTable interface {
	LiveSessionIDs(ctx context.Context) []string
	LiveSessionNames(ctx context.Context) map[string]string
}

// NewProcessTable caches one ps scan for cacheTTL. The scanner is injectable;
// the clock is injected (never a wall-clock read).
func NewProcessTable(
	scanner ProcessScanner,
	cacheTTL time.Duration,
	currentDateTime libtime.CurrentDateTimeGetter,
) ProcessTable {
	return &processTable{
		scanner:         scanner,
		cacheTTL:        cacheTTL,
		currentDateTime: currentDateTime,
	}
}

type processTable struct {
	scanner         ProcessScanner
	cacheTTL        time.Duration
	currentDateTime libtime.CurrentDateTimeGetter

	mutex    sync.Mutex
	cachedAt *libtime.DateTime
	cached   string
}

// LiveSessionIDs returns the live session-id view parsed from the cached scan.
func (t *processTable) LiveSessionIDs(ctx context.Context) []string {
	return ParseLiveSessionIDs(t.raw(ctx))
}

// LiveSessionNames returns the live name -> session-id view parsed from the
// cached scan.
func (t *processTable) LiveSessionNames(ctx context.Context) map[string]string {
	return ParseLiveSessionNames(t.raw(ctx))
}

// raw returns the raw process table, re-scanning only when the cached scan is
// older than cacheTTL. A scanner error is not fatal: it is logged at debug and
// the scan is treated as an empty table, cached like any other scan.
func (t *processTable) raw(ctx context.Context) string {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	now := t.currentDateTime.Now()
	if t.cachedAt != nil && now.Sub(*t.cachedAt).Duration() < t.cacheTTL {
		return t.cached
	}

	out, err := t.scanner(ctx)
	if err != nil {
		glog.V(4).Infof("[Session] Cannot run ps: %v", err)
		out = ""
	}
	t.cachedAt = now.Ptr()
	t.cached = out
	return out
}

func parseNames(psOutput string, flagRe *regexp.Regexp) map[string]string {
	byName := map[string]map[string]struct{}{}
	for _, line := range strings.Split(psOutput, "\n") {
		if !strings.Contains(line, "claude") {
			continue
		}
		match := flagRe.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		name, ok := parseSessionName(line)
		if !ok {
			continue
		}
		if byName[name] == nil {
			byName[name] = map[string]struct{}{}
		}
		byName[name][match[1]] = struct{}{}
	}

	names := map[string]string{}
	for name, uuids := range byName {
		if len(uuids) == 1 {
			for uuid := range uuids {
				names[name] = uuid
			}
		}
	}
	return names
}

func parseProcesses(psOutput string, flagRe *regexp.Regexp) map[string]int {
	processes := map[string]int{}
	for _, line := range strings.Split(psOutput, "\n") {
		if !strings.Contains(line, "claude") {
			continue
		}
		match := flagRe.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		processes[match[1]] = pid
	}
	return processes
}

// parseSessionName reproduces the Python _SESSION_NAME_RE
// `(?<!\w)-n\s+(?!-{1,2}[a-zA-Z])(.+?)(?=\s+-{1,2}[a-zA-Z]|\s*$)` without
// lookaround (RE2 has none). It returns the leftmost match's captured name.
//
// The `\s+` is greedy and backtracks like a regex engine: longer whitespace runs
// are tried first, and for each, the shortest name that satisfies the trailing
// lookahead is taken.
func parseSessionName(line string) (string, bool) {
	for i := 0; i+2 <= len(line); i++ {
		if line[i] != '-' || line[i+1] != 'n' {
			continue
		}
		// (?<!\w): the `-n` must not be preceded by a word character.
		if i > 0 && isWordRune(rune(line[i-1])) {
			continue
		}
		// `\s+` must match at least one whitespace rune after `-n`.
		start := i + 2
		if start >= len(line) || !isSpaceRune(rune(line[start])) {
			continue
		}
		end := start
		for end < len(line) && isSpaceRune(rune(line[end])) {
			end++
		}
		// Greedy `\s+` first, backtracking down to a single whitespace rune.
		for pos := end; pos > start; pos-- {
			// (?!-{1,2}[a-zA-Z]): the name must not start with a flag.
			if isFlagStart(line, pos) {
				continue
			}
			// (.+?) with (?=\s+-{1,2}[a-zA-Z]|\s*$): shortest name first.
			for nameEnd := pos + 1; nameEnd <= len(line); nameEnd++ {
				if endLookahead(line, nameEnd) {
					return line[pos:nameEnd], true
				}
			}
		}
	}
	return "", false
}

// isFlagStart reports whether a `-{1,2}[a-zA-Z]` flag begins at pos.
func isFlagStart(line string, pos int) bool {
	if pos >= len(line) || line[pos] != '-' {
		return false
	}
	if pos+1 < len(line) && isASCIILetter(line[pos+1]) {
		return true
	}
	return pos+2 < len(line) && line[pos+1] == '-' && isASCIILetter(line[pos+2])
}

// endLookahead reports whether either `\s+-{1,2}[a-zA-Z]` or `\s*$` holds at pos.
func endLookahead(line string, pos int) bool {
	// \s*$ — only whitespace (or nothing) remains.
	trailing := true
	for j := pos; j < len(line); j++ {
		if !isSpaceRune(rune(line[j])) {
			trailing = false
			break
		}
	}
	if trailing {
		return true
	}
	// \s+-{1,2}[a-zA-Z]
	j := pos
	for j < len(line) && isSpaceRune(rune(line[j])) {
		j++
	}
	return j > pos && isFlagStart(line, j)
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isSpaceRune(r rune) bool {
	return unicode.IsSpace(r)
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
