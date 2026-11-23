// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package vaultapi is the HTTP client for the local vault-api service — the
// single page parser vault-ui reads from once its own index is removed. It
// reads the typed list endpoints only; it holds no index, parses no page and
// spawns no process.
package vaultapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/bborbe/errors"
)

// DefaultBaseURL is the local vault-api origin when VAULT_API_URL is unset.
const DefaultBaseURL = "http://127.0.0.1:9091"

// requestTimeout bounds one list read. The board serves last-known rows on a
// timeout, so this is the ceiling before the unavailable state is entered.
const requestTimeout = 5 * time.Second

// Config is the client's base URL.
type Config struct {
	BaseURL string
}

// ConfigFromEnv reads VAULT_API_URL, defaulting to DefaultBaseURL.
func ConfigFromEnv() Config {
	base := os.Getenv("VAULT_API_URL")
	if base == "" {
		base = DefaultBaseURL
	}
	return Config{BaseURL: base}
}

// Client reads vault-api's list endpoints.
type Client interface {
	// BaseURL is the origin this client reads from, for the startup log line.
	BaseURL() string
	ListTasks(ctx context.Context) ([]Task, error)
	ListGoals(ctx context.Context) ([]Goal, error)
	ListTopics(ctx context.Context) ([]Topic, error)
}

// Task is one vault-api task item. It carries the full field set the board
// renders: the frontmatter-derived fields (priority, due/planned/defer dates,
// blocked_by, jira, mode, claude_session_id) and the Open Questions section.
type Task struct {
	ID              string         `json:"id"`
	Title           string         `json:"title"`
	Path            string         `json:"path"`
	Status          string         `json:"status"`
	Phase           string         `json:"phase"`
	TaskType        string         `json:"task_type"`
	Stage           string         `json:"stage"`
	Assignee        string         `json:"assignee"`
	Goals           []string       `json:"goals"`
	Cluster         *string        `json:"cluster"`
	Flag            bool           `json:"flag"`
	Vault           string         `json:"vault"`
	UpdatedAt       string         `json:"updated_at"`
	BlockedBy       []string       `json:"blocked_by"`
	ClaudeSessionID *string        `json:"claude_session_id"`
	DueDate         *string        `json:"due_date"`
	PlannedDate     *string        `json:"planned_date"`
	DeferDate       *string        `json:"defer_date"`
	Priority        int            `json:"priority"`
	Jira            *string        `json:"jira"`
	Mode            *string        `json:"mode"`
	OpenQuestions   []OpenQuestion `json:"open_questions"`
}

// Goal is one vault-api goal item.
type Goal struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Path            string   `json:"path"`
	Status          string   `json:"status"`
	Phase           string   `json:"phase"`
	Themes          []string `json:"themes"`
	Vault           string   `json:"vault"`
	Flag            bool     `json:"flag"`
	ClaudeSessionID *string  `json:"claude_session_id"`
	Role            string   `json:"role"`
	Mode            string   `json:"mode"`
	FlagSetBy       string   `json:"flag_set_by"`
}

// Topic is one vault-api topic item.
type Topic struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Path            string   `json:"path"`
	Status          string   `json:"status"`
	Phase           string   `json:"phase"`
	Goals           []string `json:"goals"`
	Themes          []string `json:"themes"`
	Assignee        string   `json:"assignee"`
	Vault           string   `json:"vault"`
	Flag            bool     `json:"flag"`
	ClaudeSessionID *string  `json:"claude_session_id"`
	Role            string   `json:"role"`
	Mode            string   `json:"mode"`
	FlagSetBy       string   `json:"flag_set_by"`
}

// OpenQuestion is one item of a task's Open Questions section.
type OpenQuestion struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

// taskList is the GET /api/v1/tasks envelope.
//
// warnings is a COUNT, not a list: vault-api answers `"warnings": 112` for a
// vault with that many pages it could not parse. Declaring it []string made
// every live poll fail to decode ("cannot unmarshal number into Go struct
// field taskList.warnings"), so the board served no rows at all while the
// e2e stayed green — its stub never sent the field.
type taskList struct {
	Items           []Task `json:"items"`
	Total           int    `json:"total"`
	Warnings        int    `json:"warnings"`
	IndexAgeSeconds int    `json:"index_age_seconds"`
}

// goalList is the GET /api/v1/goals envelope.
type goalList struct {
	Items []Goal `json:"items"`
}

// topicList is the GET /api/v1/topics envelope.
type topicList struct {
	Items []Topic `json:"items"`
}

type client struct {
	baseURL string
	http    *http.Client
}

// New returns a Client reading from cfg.BaseURL. One shared http.Client carries
// the deadline for every call; no call builds its own.
func New(cfg Config) Client {
	return &client{
		baseURL: cfg.BaseURL,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// BaseURL is the configured origin.
func (c *client) BaseURL() string { return c.baseURL }

// ListTasks reads GET /api/v1/tasks.
func (c *client) ListTasks(ctx context.Context) ([]Task, error) {
	var body taskList
	if err := c.get(ctx, "/api/v1/tasks", &body); err != nil {
		return nil, err
	}
	return body.Items, nil
}

// ListGoals reads GET /api/v1/goals.
func (c *client) ListGoals(ctx context.Context) ([]Goal, error) {
	var body goalList
	if err := c.get(ctx, "/api/v1/goals", &body); err != nil {
		return nil, err
	}
	return body.Items, nil
}

// ListTopics reads GET /api/v1/topics.
func (c *client) ListTopics(ctx context.Context) ([]Topic, error) {
	var body topicList
	if err := c.get(ctx, "/api/v1/topics", &body); err != nil {
		return nil, err
	}
	return body.Items, nil
}

// get performs one GET and decodes the JSON body into out.
func (c *client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return errors.Wrapf(ctx, err, "build request %s", path)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.Wrapf(ctx, err, "get %s", path)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return errors.Errorf(ctx, "get %s: status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return errors.Wrapf(ctx, err, "decode %s", path)
	}
	return nil
}
