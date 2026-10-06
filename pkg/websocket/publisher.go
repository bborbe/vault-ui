// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package websocket

import "context"

// MutationPublisher announces vault-ui write outcomes to every connected
// client. It satisfies mutations.EventPublisher structurally.
type MutationPublisher interface {
	PublishTaskUpdated(ctx context.Context, vault, taskID string)
	PublishGoalUpdated(ctx context.Context, vault, goalID string)
	PublishWriteFailed(ctx context.Context, vault, itemKind, itemID, reason string)
}

// NewMutationPublisher returns a MutationPublisher broadcasting through manager.
func NewMutationPublisher(manager ConnectionManager) MutationPublisher {
	return &mutationPublisher{manager: manager}
}

type mutationPublisher struct {
	manager ConnectionManager
}

func (p *mutationPublisher) PublishTaskUpdated(_ context.Context, vault, taskID string) {
	p.manager.Broadcast(TaskUpdatedFrame(vault, taskID))
}

func (p *mutationPublisher) PublishGoalUpdated(_ context.Context, vault, goalID string) {
	p.manager.Broadcast(GoalUpdatedFrame(vault, goalID))
}

func (p *mutationPublisher) PublishWriteFailed(
	_ context.Context, vault, itemKind, itemID, reason string,
) {
	p.manager.Broadcast(WriteFailedFrame(vault, itemKind, itemID, reason))
}
