// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package openable

import (
	"time"

	"github.com/bborbe/vault-ui/pkg/api"
)

// reasonLabels maps each wrapper reason code to its compact badge text. A code
// missing here renders as "⏸ held: <code>", so a new wrapper reason still shows.
var reasonLabels = map[string]string{
	"not-absent":                 "● live elsewhere",
	"not-absent-dead":            "◌ session lingering",
	"blocked":                    "⛔ blocked",
	"parked-until":               "⏰ parked",
	"parked-on-pr":               "🔗 waiting on PR",
	"parked-on-card":             "🃏 waiting on card",
	"progress-stalled":           "⏸ stalled",
	"recently-spawned":           "🆕 recently spawned",
	"foreign-vault":              "↗ other vault",
	"flag-provenance-unrecorded": "❔ flag provenance unrecorded",
	"spawn-refused":              "🚫 spawn refused",
}

// Label returns the badge text for a reason code. For parked-until with a
// parseable until instant it names the time, in loc: "⏰ parked until 14:00"
// on the same day as now, "⏰ parked until Nov 28 14:00" on another day.
func Label(reason string, until string, now time.Time, loc *time.Location) string {
	if reason == "parked-until" && until != "" {
		if at, err := time.Parse(time.RFC3339, until); err == nil {
			local := at.In(loc)
			nowLocal := now.In(loc)
			if local.Year() == nowLocal.Year() && local.YearDay() == nowLocal.YearDay() {
				return "⏰ parked until " + local.Format("15:04")
			}
			return "⏰ parked until " + local.Format("Jan 2 15:04")
		}
	}
	if label, ok := reasonLabels[reason]; ok {
		return label
	}
	if reason == "" {
		return "⏸ held"
	}
	return "⏸ held: " + reason
}

// NewHold renders a held row as the wire shape. It returns nil for a row the
// wrapper does not hold.
func NewHold(row Row, now time.Time, loc *time.Location) *api.HoldResponse {
	if row.Decision != DecisionHeld {
		return nil
	}
	waiting := WaitingOn{}
	if row.WaitingOn != nil {
		waiting = *row.WaitingOn
	}
	blockers := waiting.Blockers
	if blockers == nil {
		blockers = []string{}
	}
	return &api.HoldResponse{
		Reason:    row.Reason,
		Label:     Label(row.Reason, waiting.Until, now, loc),
		Summary:   nonEmpty(waiting.Summary),
		Since:     nonEmpty(waiting.Since),
		Until:     nonEmpty(waiting.Until),
		CardID:    nonEmpty(waiting.CardID),
		PRURL:     nonEmpty(waiting.PRURL),
		Blockers:  blockers,
		SessionID: nonEmpty(waiting.SessionID),
	}
}

func nonEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
