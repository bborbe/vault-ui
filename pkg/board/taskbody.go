// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/bborbe/errors"
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/api"
)

// TaskListBody is a rendered GET /api/tasks response in the two forms the
// handler can serve: Identity is the JSON body the board produces today,
// Gzipped is its gzip-compressed form. Both are immutable once returned and are
// shared with every reader.
type TaskListBody struct {
	Identity []byte
	Gzipped  []byte
}

// taskBodyParams carries the body cache's injectable dependencies.
type taskBodyParams struct {
	// Project renders the query's rows. Production: the board's ListTasks.
	Project func(ctx context.Context, query TaskQuery) ([]api.TaskResponse, error)
	// Encode marshals the projected rows into the identity body. Production:
	// encodeTaskList, which reproduces the handler's writeJSON exactly.
	Encode func(value any) ([]byte, error)
	// Generations reports the task-list snapshot generation.
	Generations func() uint64
}

// taskBodyEntry is one held body. It is replaced wholesale on invalidation
// rather than mutated in place.
type taskBodyEntry struct {
	body TaskListBody
}

// taskBodyCache holds one rendered body per (snapshot generation, query). A
// superseded generation's entries are dropped rather than retained, so the cache
// holds at most one generation's bodies.
type taskBodyCache struct {
	mu          sync.Mutex
	generation  uint64
	entries     map[string]*taskBodyEntry
	project     func(ctx context.Context, query TaskQuery) ([]api.TaskResponse, error)
	encode      func(value any) ([]byte, error)
	generations func() uint64
}

// newTaskBodyCache returns an empty cache over the given seams.
func newTaskBodyCache(params taskBodyParams) *taskBodyCache {
	return &taskBodyCache{
		entries:     map[string]*taskBodyEntry{},
		project:     params.Project,
		encode:      params.Encode,
		generations: params.Generations,
	}
}

// Get returns the rendered body for the query, building and holding it on a
// miss. A hit performs no projection, no marshal and no compression. A
// generation move drops the superseded generation's entries; a build that
// started before a generation move is not held.
func (c *taskBodyCache) Get(ctx context.Context, query TaskQuery) (TaskListBody, error) {
	key := taskBodyKey(query)

	c.mu.Lock()
	gen := c.generations()
	if gen != c.generation {
		c.entries = map[string]*taskBodyEntry{}
		c.generation = gen
	}
	if entry, ok := c.entries[key]; ok {
		body := entry.body
		c.mu.Unlock()
		return body, nil
	}
	c.mu.Unlock()

	responses, err := c.project(ctx, query)
	if err != nil {
		glog.Errorf("build task-list body for key %q query %+v: %v", key, query, err)
		return TaskListBody{}, errors.Wrapf(ctx, err, "build task-list body")
	}
	identity, err := c.encode(responses)
	if err != nil {
		glog.Errorf("encode task-list body for key %q query %+v: %v", key, query, err)
		return TaskListBody{}, errors.Wrapf(ctx, err, "build task-list body")
	}
	gzipped, err := gzipBody(identity)
	if err != nil {
		glog.Errorf("gzip task-list body for key %q query %+v: %v", key, query, err)
		return TaskListBody{}, errors.Wrapf(ctx, err, "build task-list body")
	}

	body := TaskListBody{Identity: identity, Gzipped: gzipped}

	c.mu.Lock()
	if c.generations() == gen {
		c.entries[key] = &taskBodyEntry{body: body}
	}
	c.mu.Unlock()

	return body, nil
}

// taskBodyKey is a canonical key over every query field that changes the body,
// including upcoming_hours and session_live. It must distinguish repeated values
// and their order, because selectVaults preserves the requested order and the
// body follows it.
func taskBodyKey(query TaskQuery) string {
	return fmt.Sprintf("%q|%q|%q|%q|%q|%d|%t",
		query.Vaults,
		query.Statuses,
		query.Phases,
		query.Assignees,
		query.Goals,
		query.UpcomingHours,
		query.SessionLive,
	)
}

// encodeTaskList marshals the projected rows exactly as the handler's writeJSON
// did: encoding/json with SetEscapeHTML(false) and the encoder's trailing
// newline trimmed. The bytes must match the handler's old output byte-for-byte,
// so the decoded body is unchanged.
func encodeTaskList(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// gzipBody compresses the identity body. It is the only place the board
// compresses a task-list body.
func gzipBody(identity []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(identity); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
