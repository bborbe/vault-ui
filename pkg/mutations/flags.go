// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mutations

import (
	"context"

	"github.com/bborbe/vault-cli/pkg/ops"

	"github.com/bborbe/vault-ui/pkg/vaultconfig"
)

// flagSetOperation and flagClearOperation are the frontmatter shapes the flag
// write body calls. They are the common subset of vault-cli's two operation
// families: the task family (FrontmatterSetOperation/FrontmatterClearOperation,
// which carries the flag provenance actor) and the entity family
// (EntitySetOperation/EntityClearOperation, shared by goals and topics). One
// write body against these interfaces is what keeps the flag path
// kind-parameterized rather than forked per kind.
type flagSetOperation interface {
	Execute(
		ctx context.Context, vaultPath, itemName, key, value, reason, gateSuccessor string,
	) error
}

type flagClearOperation interface {
	Execute(ctx context.Context, vaultPath, itemName, key string) error
}

// flagOps is the kind-resolved pair the flag write body runs.
type flagOps struct {
	FrontmatterSet   flagSetOperation
	FrontmatterClear flagClearOperation
}

// taskFlagSet adapts the task FrontmatterSetOperation — which takes the flag
// provenance actor and a force flag — to the common flag-set shape. The actor
// stays "operator" and force stays false, exactly as the task-only flag path
// wrote them before the kinds were unified.
type taskFlagSet struct {
	op ops.FrontmatterSetOperation
}

func (t taskFlagSet) Execute(
	ctx context.Context, vaultPath, itemName, key, value, reason, gateSuccessor string,
) error {
	return t.op.Execute(ctx, vaultPath, itemName, key, value, reason, gateSuccessor, "operator", false)
}

// flagOps resolves the frontmatter operations one kind's flag writes go
// through. An unknown kind reports false so the caller can answer 400.
func (s *service) flagOps(vault vaultconfig.Vault, kind string) (flagOps, bool) {
	set := s.opsForVault(vault)
	switch kind {
	case "task":
		return flagOps{
			FrontmatterSet:   taskFlagSet{op: set.FrontmatterSet},
			FrontmatterClear: set.FrontmatterClear,
		}, true
	case "goal":
		return flagOps{
			FrontmatterSet:   set.GoalSet,
			FrontmatterClear: set.GoalClear,
		}, true
	case "topic":
		return flagOps{
			FrontmatterSet:   set.TopicSet,
			FrontmatterClear: set.TopicClear,
		}, true
	}
	return flagOps{}, false
}

// itemWritten is the post-write side-effect of the flag route for one kind: the
// kind's own cache invalidation, page-index dirty mark and update frame.
func (s *service) itemWritten(
	vault vaultconfig.Vault, kind, itemID string,
) func(context.Context) {
	switch kind {
	case "goal":
		return s.goalWritten(vault, itemID)
	case "topic":
		return s.topicWritten(vault, itemID)
	default:
		return s.taskWritten(vault, itemID)
	}
}
