// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package websocket

import (
	"encoding/json"

	"github.com/bborbe/vault-cli/pkg/ops"
)

// watcherFrame is the frame the Python backend broadcasts for a vault watcher
// event (src/vault_ui/factory.py on_change). The identifier key is task_id even
// for goals, themes, and objectives — the Python reuses the key here.
type watcherFrame struct {
	Type     string `json:"type"`
	TaskID   string `json:"task_id"`
	Vault    string `json:"vault"`
	ItemKind string `json:"item_kind"`
}

// taskUpdatedFrame is the frame the Python task mutating routes broadcast.
type taskUpdatedFrame struct {
	Type     string `json:"type"`
	TaskID   string `json:"task_id"`
	ItemKind string `json:"item_kind"`
	Vault    string `json:"vault"`
}

// goalUpdatedFrame is the frame the Python goal mutating routes broadcast. The
// identifier key is goal_id — the goal family differs from the task family.
type goalUpdatedFrame struct {
	Type     string `json:"type"`
	GoalID   string `json:"goal_id"`
	ItemKind string `json:"item_kind"`
	Vault    string `json:"vault"`
}

// WatcherFrame builds the frame for a vault watcher event. type carries the
// change event (modified/created/deleted); item_kind carries the entity type.
func WatcherFrame(event ops.WatchEvent) []byte {
	return marshal(watcherFrame{
		Type:     selftestWatcherEvent(event.Event),
		TaskID:   event.Name,
		Vault:    event.Vault,
		ItemKind: event.Type,
	})
}

// TaskUpdatedFrame builds the frame for a task mutation.
func TaskUpdatedFrame(vault, taskID string) []byte {
	return marshal(taskUpdatedFrame{
		Type:     "task_updated",
		TaskID:   taskID,
		ItemKind: "task",
		Vault:    vault,
	})
}

// GoalUpdatedFrame builds the frame for a goal mutation.
func GoalUpdatedFrame(vault, goalID string) []byte {
	return marshal(goalUpdatedFrame{
		Type:     "goal_updated",
		GoalID:   goalID,
		ItemKind: "goal",
		Vault:    vault,
	})
}

// writeFailedFrame is broadcast when a queued vault write fails. Like the
// watcher frame, the identifier key is task_id for every item kind.
type writeFailedFrame struct {
	Type     string `json:"type"`
	TaskID   string `json:"task_id"`
	ItemKind string `json:"item_kind"`
	Vault    string `json:"vault"`
	Reason   string `json:"reason"`
}

// WriteFailedFrame builds the frame for a failed queued write. itemKind is
// "task" or "goal".
func WriteFailedFrame(vault, itemKind, itemID, reason string) []byte {
	return marshal(writeFailedFrame{
		Type:     "write_failed",
		TaskID:   itemID,
		ItemKind: itemKind,
		Vault:    vault,
		Reason:   reason,
	})
}

// marshal encodes a frame. The frame structs are fixed-shape and cannot fail to
// marshal.
func marshal(frame any) []byte {
	data, err := json.Marshal(frame)
	if err != nil {
		return []byte("{}")
	}
	return data
}
