// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"

	stderrors "errors"

	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/board"
)

// writeJSON writes value as a compact JSON response with the given status.
// HTML characters are not escaped, matching the Python JSONResponse encoder.
func writeJSON(resp http.ResponseWriter, status int, value any) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		return
	}
	body := bytes.TrimRight(buffer.Bytes(), "\n")
	resp.Header().Set("Content-Type", "application/json")
	resp.WriteHeader(status)
	_, _ = resp.Write(body)
}

// writeDetail writes the framework-shaped error body {"detail": "..."}.
func writeDetail(resp http.ResponseWriter, status int, detail string) {
	writeJSON(resp, status, api.DetailResponse{Detail: detail})
}

// writeNotFound writes the FastAPI default 404 body.
func writeNotFound(resp http.ResponseWriter) {
	writeDetail(resp, http.StatusNotFound, "Not Found")
}

// validationItem is one FastAPI/Pydantic validation error entry.
type validationItem struct {
	Type  string         `json:"type"`
	Loc   []string       `json:"loc"`
	Msg   string         `json:"msg"`
	Input any            `json:"input"`
	Ctx   map[string]any `json:"ctx,omitempty"`
}

// validationBody is the FastAPI 422 response body.
type validationBody struct {
	Detail []validationItem `json:"detail"`
}

// writeValidation writes a FastAPI-shaped 422 response.
func writeValidation(resp http.ResponseWriter, items []validationItem) {
	writeJSON(resp, http.StatusUnprocessableEntity, validationBody{Detail: items})
}

// missingQueryError is the FastAPI 422 entry for a missing required query param.
func missingQueryError(name string) validationItem {
	return validationItem{
		Type:  "missing",
		Loc:   []string{"query", name},
		Msg:   "Field required",
		Input: nil,
	}
}

// intRangeError is the FastAPI 422 entry for an out-of-range integer query
// param. bound is "le" or "ge" and limit is the inclusive bound.
func intRangeError(name, raw, bound string, limit int) validationItem {
	if bound == "le" {
		return validationItem{
			Type:  "less_than_equal",
			Loc:   []string{"query", name},
			Msg:   "Input should be less than or equal to " + strconv.Itoa(limit),
			Input: raw,
			Ctx:   map[string]any{"le": limit},
		}
	}
	return validationItem{
		Type:  "greater_than_equal",
		Loc:   []string{"query", name},
		Msg:   "Input should be greater than or equal to " + strconv.Itoa(limit),
		Input: raw,
		Ctx:   map[string]any{"ge": limit},
	}
}

// intParseError is the FastAPI 422 entry for an unparseable integer query param.
func intParseError(name, raw string) validationItem {
	return validationItem{
		Type:  "int_parsing",
		Loc:   []string{"query", name},
		Msg:   "Input should be a valid integer, unable to parse string as an integer",
		Input: raw,
	}
}

// parseUpcomingHours reads and range-checks the upcoming_hours query param
// (default 8, inclusive range 0..168), returning the FastAPI-shaped error entry
// when invalid.
func parseUpcomingHours(query map[string][]string) (int, *validationItem) {
	values, ok := query["upcoming_hours"]
	if !ok || len(values) == 0 {
		return board.DefaultUpcomingHours, nil
	}
	raw := values[len(values)-1]
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		item := intParseError("upcoming_hours", raw)
		return 0, &item
	}
	if parsed < 0 {
		item := intRangeError("upcoming_hours", raw, "ge", 0)
		return 0, &item
	}
	if parsed > 168 {
		item := intRangeError("upcoming_hours", raw, "le", 168)
		return 0, &item
	}
	return parsed, nil
}

// parseBool reproduces the FastAPI bool query coercion: "true"/"1"/"yes"/"on"
// (case-insensitive) are true, everything else is false.
func parseBool(raw string) bool {
	value, err := strconv.ParseBool(raw)
	if err == nil {
		return value
	}
	switch raw {
	case "yes", "on", "Yes", "On", "YES", "ON":
		return true
	default:
		return false
	}
}

// writeBoardError maps a board error to the Python status/body contract.
func writeBoardError(resp http.ResponseWriter, err error) {
	var unknownVault board.UnknownVaultError
	if stderrors.As(err, &unknownVault) {
		writeDetail(resp, http.StatusNotFound, unknownVault.Error())
		return
	}
	var topicNotFound board.TopicNotFoundError
	if stderrors.As(err, &topicNotFound) {
		writeDetail(resp, http.StatusNotFound, topicNotFound.Error())
		return
	}
	writeDetail(resp, http.StatusInternalServerError, err.Error())
}
