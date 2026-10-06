// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package websocket_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/websocket"
	"github.com/bborbe/vault-ui/pkg/websocket/mocks"
)

var _ = Describe("MutationPublisher", func() {
	var (
		manager   *mocks.WebsocketConnectionManager
		publisher websocket.MutationPublisher
		ctx       context.Context
	)

	BeforeEach(func() {
		manager = &mocks.WebsocketConnectionManager{}
		publisher = websocket.NewMutationPublisher(manager)
		ctx = context.Background()
	})

	It("broadcasts exactly the task-updated frame", func() {
		publisher.PublishTaskUpdated(ctx, "personal", "TaskOne")
		Expect(manager.BroadcastCallCount()).To(Equal(1))
		Expect(manager.BroadcastArgsForCall(0)).
			To(Equal(websocket.TaskUpdatedFrame("personal", "TaskOne")))
	})

	It("broadcasts exactly the goal-updated frame", func() {
		publisher.PublishGoalUpdated(ctx, "personal", "GoalOne")
		Expect(manager.BroadcastCallCount()).To(Equal(1))
		Expect(manager.BroadcastArgsForCall(0)).
			To(Equal(websocket.GoalUpdatedFrame("personal", "GoalOne")))
	})

	It("broadcasts exactly the write-failed frame", func() {
		publisher.PublishWriteFailed(ctx, "personal", "goal", "GoalOne", "permission denied")
		Expect(manager.BroadcastCallCount()).To(Equal(1))
		Expect(manager.BroadcastArgsForCall(0)).
			To(Equal(websocket.WriteFailedFrame("personal", "goal", "GoalOne", "permission denied")))
	})
})
