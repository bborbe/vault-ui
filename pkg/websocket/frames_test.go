// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package websocket_test

import (
	"encoding/json"

	"github.com/bborbe/vault-cli/pkg/ops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/websocket"
)

var _ = Describe("Frames", func() {
	DescribeTable("WatcherFrame mirrors the Python watcher frame",
		func(event ops.WatchEvent, expected map[string]string) {
			var frame map[string]string
			Expect(json.Unmarshal(websocket.WatcherFrame(event), &frame)).To(Succeed())
			Expect(frame).To(Equal(expected))
		},
		Entry("task modified",
			ops.WatchEvent{Event: "modified", Name: "TaskOne", Vault: "personal", Type: "task"},
			map[string]string{
				"type": "modified", "task_id": "TaskOne", "vault": "personal", "item_kind": "task",
			},
		),
		Entry("goal created",
			ops.WatchEvent{Event: "created", Name: "GoalOne", Vault: "personal", Type: "goal"},
			map[string]string{
				"type": "created", "task_id": "GoalOne", "vault": "personal", "item_kind": "goal",
			},
		),
		Entry("theme deleted",
			ops.WatchEvent{Event: "deleted", Name: "ThemeOne", Vault: "personal", Type: "theme"},
			map[string]string{
				"type": "deleted", "task_id": "ThemeOne", "vault": "personal", "item_kind": "theme",
			},
		),
		Entry("objective modified",
			ops.WatchEvent{Event: "modified", Name: "ObjOne", Vault: "personal", Type: "objective"},
			map[string]string{
				"type": "modified", "task_id": "ObjOne", "vault": "personal", "item_kind": "objective",
			},
		),
	)

	Describe("TaskUpdatedFrame", func() {
		It("carries task_id and the task item_kind", func() {
			var frame map[string]string
			Expect(json.Unmarshal(websocket.TaskUpdatedFrame("personal", "TaskOne"), &frame)).
				To(Succeed())
			Expect(frame).To(Equal(map[string]string{
				"type":      "task_updated",
				"task_id":   "TaskOne",
				"item_kind": "task",
				"vault":     "personal",
			}))
		})
	})

	Describe("WriteFailedFrame", func() {
		It("carries task_id, item_kind, vault and reason in fixed order", func() {
			Expect(string(websocket.WriteFailedFrame(
				"personal", "task", "Task A", "permission denied",
			))).To(Equal(
				`{"type":"write_failed","task_id":"Task A","item_kind":"task",` +
					`"vault":"personal","reason":"permission denied"}`,
			))
		})
	})

	Describe("GoalUpdatedFrame", func() {
		It("carries goal_id and the goal item_kind", func() {
			var frame map[string]string
			Expect(json.Unmarshal(websocket.GoalUpdatedFrame("personal", "GoalOne"), &frame)).
				To(Succeed())
			Expect(frame).To(Equal(map[string]string{
				"type":      "goal_updated",
				"goal_id":   "GoalOne",
				"item_kind": "goal",
				"vault":     "personal",
			}))
		})
	})
})
