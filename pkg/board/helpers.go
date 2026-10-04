// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board

import (
	"strings"
	"time"

	libtime "github.com/bborbe/time"
)

// flattenFilter reproduces Python `_flatten_filter`: each repeated value is
// additionally comma-split and trimmed, empty tokens are dropped, and an empty
// result collapses to nil.
func flattenFilter(values []string) []string {
	if values == nil {
		return nil
	}
	flat := make([]string, 0, len(values))
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			token = strings.TrimSpace(token)
			if token != "" {
				flat = append(flat, token)
			}
		}
	}
	if len(flat) == 0 {
		return nil
	}
	return flat
}

// flattenAssigneeFilter reproduces Python `_flatten_assignee_filter`: comma-split
// and trimmed, but empty tokens are kept because an empty token matches
// unassigned work.
func flattenAssigneeFilter(values []string) []string {
	if values == nil {
		return nil
	}
	flat := make([]string, 0, len(values))
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			flat = append(flat, strings.TrimSpace(token))
		}
	}
	return flat
}

// strPtr returns nil for the empty string, else a pointer to it. vault-cli
// omits empty optional fields, which Python reads as None.
func strPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// parseDateTime parses vault-cli's "2006-01-02T15:04:05Z" UTC form, returning
// nil for an empty or unparseable value.
func parseDateTime(value string) *libtime.DateTime {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	utc := libtime.DateTime(parsed.UTC())
	return &utc
}

// parseDeferDate reproduces Python `_parse_defer_date`: a date-only value is
// midnight UTC on that date, an RFC3339 datetime is taken as-is (UTC when
// naive).
func parseDeferDate(value string) (time.Time, bool) {
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		return parsed.UTC(), true
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC(), true
	}
	return time.Time{}, false
}

// stripWikilink removes a surrounding [[...]] wrapper, mirroring the Python
// goal-name strip.
func stripWikilink(value string) string {
	if strings.HasPrefix(value, "[[") && strings.HasSuffix(value, "]]") {
		return value[2 : len(value)-2]
	}
	return value
}

// stripBrackets removes any leading/trailing square brackets and whitespace,
// mirroring Python `blocker_wikilink.strip("[]").strip()`.
func stripBrackets(value string) string {
	return strings.TrimSpace(strings.Trim(value, "[]"))
}

// pythonQuote reproduces Python urllib.parse.quote with the default safe='/':
// unreserved characters (letters, digits, '_', '.', '-', '~') and '/' are kept,
// everything else is percent-encoded with uppercase hex.
func pythonQuote(value string) string {
	const hex = "0123456789ABCDEF"
	var builder strings.Builder
	for i := 0; i < len(value); i++ {
		b := value[i]
		if isUnreserved(b) {
			builder.WriteByte(b)
			continue
		}
		builder.WriteByte('%')
		builder.WriteByte(hex[b>>4])
		builder.WriteByte(hex[b&0x0f])
	}
	return builder.String()
}

func isUnreserved(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z',
		b >= 'A' && b <= 'Z',
		b >= '0' && b <= '9',
		b == '_', b == '.', b == '-', b == '~', b == '/':
		return true
	default:
		return false
	}
}

// obsidianURL builds the Obsidian deep link, matching the Python shape
// `obsidian://open?vault=<quote(vault)>&file=<quote(file)>`.
func obsidianURL(vaultName, filePath string) string {
	return "obsidian://open?vault=" + pythonQuote(vaultName) + "&file=" + pythonQuote(filePath)
}

// hasString reports whether values contains needle (exact match).
func hasString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
