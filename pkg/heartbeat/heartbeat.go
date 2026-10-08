// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package heartbeat reads the attention-controller's session-heartbeat API: the
// store's own record of which Claude sessions are live.
//
// The store is the liveness source the board classifies from. It is not the
// harness session registry under `~/.claude/sessions` the board used to read:
// that directory only sees sessions started on this host, while the store also
// carries the heartbeats a cluster session posts, and it computes the liveness
// window itself (`live`) rather than leaving each reader to recompute it from
// `age_seconds`.
//
// Two outcomes are deliberately distinguished and must never be conflated:
//
//   - The store answers. Every id it reports `live: true` is live; every id it
//     does not report is not live, and a Resume for such a session is safe.
//   - The store cannot be reached. That is "cannot tell", not "nothing is
//     live": a session that is live but idle (its transcript has stopped being
//     written and no `--resume` process pins it) would otherwise render Resume.
//     Every reader therefore reports reachability alongside the ids.
package heartbeat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang/glog"
)

// DefaultBaseURL is the attention store's default base URL. The production
// value comes from ATTENTION_STORE_URL.
const DefaultBaseURL = "http://localhost:18080"

// DefaultTimeout bounds one heartbeat request. The store is a local endpoint, so
// a request that outlives this is a hung store, not a slow one.
const DefaultTimeout = 5 * time.Second

// sessionHeartbeatPath is the store's session-heartbeat collection endpoint. A
// session id is appended for the single-session form.
const sessionHeartbeatPath = "/api/1.0/session-heartbeat"

// Store is the attention-controller's session-heartbeat API.
type Store interface {
	// LiveSessionIDs returns the session ids the store currently reports live.
	//
	// The second result is false when the store could not be reached — "cannot
	// tell", which a caller must never read as "nothing is live". A store that
	// answers returns (ids, true), and ids may be empty: an answered store with
	// no live sessions is not the same as an unreachable one.
	LiveSessionIDs(ctx context.Context) ([]string, bool)

	// IsLive reports the store's verdict for one session. A 404 is
	// (false, true): the session has never posted a heartbeat, so it is not
	// live and a Resume is safe. The second result is false only when the store
	// could not be reached — the same "cannot tell" as LiveSessionIDs.
	IsLive(ctx context.Context, sessionID string) (bool, bool)
}

// NewStore creates a Store for baseURL. A blank baseURL falls back to
// DefaultBaseURL; a nil client falls back to one bounded by DefaultTimeout.
func NewStore(baseURL string, client *http.Client) Store {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	return &store{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  client,
	}
}

type store struct {
	baseURL string
	client  *http.Client
}

// heartbeatRow is one row of the session-heartbeat payload. Only the fields
// vault-ui reads are declared; the store may carry more.
type heartbeatRow struct {
	SessionID string `json:"session_id"`
	Live      bool   `json:"live"`
}

// LiveSessionIDs lists the store's live session ids. Every failure — a
// transport error, a non-200 status, an unparseable body — is "cannot tell"
// (nil, false), never an empty live set.
func (s *store) LiveSessionIDs(ctx context.Context) ([]string, bool) {
	body, ok := s.get(ctx, s.baseURL+sessionHeartbeatPath)
	if !ok {
		return nil, false
	}

	var rows []heartbeatRow
	if err := json.Unmarshal(body, &rows); err != nil {
		glog.V(4).Infof("[Heartbeat] Cannot parse session-heartbeat payload: %v", err)
		return nil, false
	}

	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Live && row.SessionID != "" {
			ids = append(ids, row.SessionID)
		}
	}
	return ids, true
}

// IsLive reports whether the store lists one session as live. A 404 is
// (false, true) — the session has never posted, so it is not live; any other
// failure is (false, false) — cannot tell.
func (s *store) IsLive(ctx context.Context, sessionID string) (bool, bool) {
	if sessionID == "" {
		return false, true
	}

	endpoint := s.baseURL + sessionHeartbeatPath + "/" + url.PathEscape(sessionID)
	body, status, ok := s.getStatus(ctx, endpoint)
	if !ok {
		return false, false
	}
	if status == http.StatusNotFound {
		return false, true
	}
	if status != http.StatusOK {
		glog.V(4).Infof("[Heartbeat] session-heartbeat returned HTTP %d", status)
		return false, false
	}

	var row heartbeatRow
	if err := json.Unmarshal(body, &row); err != nil {
		glog.V(4).Infof("[Heartbeat] Cannot parse session-heartbeat row: %v", err)
		return false, false
	}
	return row.Live, true
}

// get performs one GET and returns the body when the response is 200. Any other
// outcome is (nil, false).
func (s *store) get(ctx context.Context, endpoint string) ([]byte, bool) {
	body, status, ok := s.getStatus(ctx, endpoint)
	if !ok || status != http.StatusOK {
		if ok {
			glog.V(4).Infof("[Heartbeat] %s returned HTTP %d", endpoint, status)
		}
		return nil, false
	}
	return body, true
}

// getStatus performs one GET and returns the body and status. The third result
// is false for a transport failure or an unreadable body — the store was never
// reached, so no status is available.
func (s *store) getStatus(ctx context.Context, endpoint string) ([]byte, int, bool) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		glog.V(4).Infof("[Heartbeat] Cannot build request for %s: %v", endpoint, err)
		return nil, 0, false
	}

	response, err := s.client.Do(request)
	if err != nil {
		glog.V(4).Infof("[Heartbeat] Cannot reach attention store at %s: %v", endpoint, err)
		return nil, 0, false
	}
	defer func() { _ = response.Body.Close() }()

	body, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		glog.V(4).Infof("[Heartbeat] Cannot read %s: %v", endpoint, readErr)
		return nil, response.StatusCode, false
	}
	return body, response.StatusCode, true
}
