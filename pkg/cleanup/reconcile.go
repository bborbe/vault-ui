// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cleanup

import (
	"time"

	libtime "github.com/bborbe/time"
)

// markerTimeLayouts are the layouts accepted for a claude_session_started
// marker. RFC3339Nano covers the ISO-8601 instant with an offset that the launch
// path writes; the zone-less layouts cover a naive timestamp, which Python
// assumes is UTC.
var markerTimeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
}

// markerAgeSeconds returns the age in seconds of a claude_session_started marker
// and whether the marker carried a parseable instant. It returns ok=false only
// when the marker carries no parseable instant; that covers the legacy literal
// "true" written before 2026-08-29, and callers treat an unknown age as expired.
func markerAgeSeconds(marker string, now libtime.DateTime) (float64, bool) {
	started, ok := parseMarkerTime(marker)
	if !ok {
		return 0, false
	}
	age := now.UTC().Sub(libtime.DateTime(started.UTC())).Duration()
	return age.Seconds(), true
}

// parseMarkerTime parses a marker into a time.Time. A naive timestamp is
// interpreted as UTC, exactly as Python's datetime.replace(tzinfo=UTC) does.
func parseMarkerTime(marker string) (time.Time, bool) {
	for _, layout := range markerTimeLayouts {
		if parsed, err := time.Parse(layout, marker); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}
