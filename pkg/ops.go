// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultui

import "github.com/bborbe/vault-cli/pkg/ops"

// OpSet is the full set of vault-cli operations the backend needs, wired once
// from a single vault. List is used with the tasks, goals, and topics
// directories (the directory is passed to Execute, not to the constructor).
type OpSet struct {
	List             ops.ListOperation
	Show             ops.ShowOperation
	FrontmatterSet   ops.FrontmatterSetOperation
	FrontmatterClear ops.FrontmatterClearOperation
	WorkOn           ops.WorkOnOperation
	Approve          ops.TaskApproveOperation
	Defer            ops.DeferOperation
	Complete         ops.CompleteOperation
	GoalSet          ops.EntitySetOperation
	GoalClear        ops.EntityClearOperation
	GoalWorkOn       ops.GoalWorkOnOperation
	GoalDefer        ops.GoalDeferOperation
	GoalComplete     ops.GoalCompleteOperation
	TopicShow        ops.EntityShowOperation
	TopicSet         ops.EntitySetOperation
	TopicClear       ops.EntityClearOperation
}
