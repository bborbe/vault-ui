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

// flagTarget is one kind's resolved flag write: the frontmatter operations the
// write body runs, and the post-write side-effect. Both are resolved in the
// same switch so a kind cannot write through one family and publish another —
// the failure mode when they are two independent switches with a silent
// default.
type flagTarget struct {
	ops     flagOps
	written func(context.Context)
}

// taskFlagSet adapts the task FrontmatterSetOperation — which takes the flag
// provenance actor and a force flag — to the common flag-set shape. The actor
// stays "operator" and force stays false, exactly as the task-only flag path
// wrote them before the kinds were unified.
//
// The entity family (goals, topics) records the same "operator" actor through
// entityFlagSet.
type taskFlagSet struct {
	op ops.FrontmatterSetOperation
}

func (t taskFlagSet) Execute(
	ctx context.Context, vaultPath, itemName, key, value, reason, gateSuccessor string,
) error {
	return t.op.Execute(
		ctx, vaultPath, itemName, key, value, reason, gateSuccessor, "operator", false,
	)
}

// entityFlagSet adapts the entity EntitySetOperation (goals and topics) to the
// common flag-set shape. vault-cli refuses a goal flag write with a blank actor,
// so the actor is "operator", the same value the task path records.
type entityFlagSet struct {
	op ops.EntitySetOperation
}

func (e entityFlagSet) Execute(
	ctx context.Context, vaultPath, itemName, key, value, reason, gateSuccessor string,
) error {
	return e.op.Execute(ctx, vaultPath, itemName, key, value, reason, gateSuccessor, "operator")
}

// flagTarget resolves the frontmatter operations and post-write side-effect one
// kind's flag writes go through. An unknown kind reports false so the caller
// can answer 400.
func (s *service) flagTarget(
	vault vaultconfig.Vault, kind, itemID string,
) (flagTarget, bool) {
	set := s.opsForVault(vault)
	switch kind {
	case "task":
		return flagTarget{
			ops: flagOps{
				FrontmatterSet:   taskFlagSet{op: set.FrontmatterSet},
				FrontmatterClear: set.FrontmatterClear,
			},
			written: s.taskWritten(vault, itemID),
		}, true
	case "goal":
		return flagTarget{
			ops: flagOps{
				FrontmatterSet:   entityFlagSet{op: set.GoalSet},
				FrontmatterClear: set.GoalClear,
			},
			written: s.goalWritten(vault, itemID),
		}, true
	case "topic":
		return flagTarget{
			ops: flagOps{
				FrontmatterSet:   entityFlagSet{op: set.TopicSet},
				FrontmatterClear: set.TopicClear,
			},
			written: s.topicWritten(vault, itemID),
		}, true
	}
	return flagTarget{}, false
}
