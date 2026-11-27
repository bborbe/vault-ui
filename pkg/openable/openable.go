// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package openable reads claude-wrapper's openable endpoint — the wrapper's
// per-row verdict on whether its flagged tick opens a session for a flagged
// task, goal or topic, and why it holds the ones it does not open — and keeps a
// briefly cached, fail-open index of the held rows for the board.
//
// Fail-open is the contract: a wrapper that cannot be reached, answers non-200
// or sends an unparseable body yields no holds at all, never an error, so the
// board renders exactly what it rendered before this package existed.
package openable

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bborbe/errors"
)

// DefaultBaseURL is claude-wrapper's default base URL. The production value
// comes from CLAUDE_WRAPPER_URL.
const DefaultBaseURL = "http://localhost:18090"

// DefaultTimeout bounds one openable request. The wrapper is a local endpoint.
const DefaultTimeout = 3 * time.Second

// openablePath is the wrapper's openable endpoint.
const openablePath = "/api/1.0/openable"

// DecisionHeld is the decision the wrapper reports for a row it does not open.
const DecisionHeld = "held"

// WaitingOn is the optional detail the wrapper attaches to a held row. Every
// field is optional; a wrapper that predates it sends none.
type WaitingOn struct {
	Summary   string   `json:"summary"`
	Since     string   `json:"since"`
	Until     string   `json:"until"`
	CardID    string   `json:"card_id"`
	PRURL     string   `json:"pr_url"`
	Blockers  []string `json:"blockers"`
	SessionID string   `json:"session_id"`
}

// Row is one openable row. Only the fields vault-ui reads are declared.
type Row struct {
	Vault     string     `json:"vault"`
	Kind      string     `json:"kind"`
	Name      string     `json:"name"`
	SessionID string     `json:"session_id"`
	Decision  string     `json:"decision"`
	Reason    string     `json:"reason"`
	Phase     string     `json:"phase"`
	WaitingOn *WaitingOn `json:"waiting_on"`
}

type envelope struct {
	Rows []Row `json:"rows"`
}

// Client reads the wrapper's openable rows.
type Client interface {
	Rows(ctx context.Context) ([]Row, error)
}

// NewClient creates a Client for baseURL. A blank baseURL falls back to
// DefaultBaseURL; a nil client falls back to one bounded by DefaultTimeout.
func NewClient(baseURL string, client *http.Client) Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	return &httpClient{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

type httpClient struct {
	baseURL string
	client  *http.Client
}

// Rows performs one GET of the openable endpoint.
func (c *httpClient) Rows(ctx context.Context) ([]Row, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+openablePath, nil)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "build openable request")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "get openable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.Errorf(ctx, "openable returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "read openable body")
	}
	var payload envelope
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.Wrap(ctx, err, "parse openable body")
	}
	return payload.Rows, nil
}
