// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/vaultconfig"
)

// cleanupAdapterFixture builds the production VaultOps adapter over a temp vault
// seeded with one task and one goal.
func cleanupAdapterFixture() (cleanupVaultOps, string) {
	dir := GinkgoT().TempDir()
	writeVaultFile(dir, "24 Tasks/Task A.md", "---\nstatus: next\nassignee: alice\n---\n# Task A\n")
	writeVaultFile(dir, "23 Goals/Goal A.md", "---\nstatus: in_progress\n---\n# Goal A\n")

	resolved := vaultconfig.Vault{
		Name: "test", Path: dir, TasksFolder: "24 Tasks", GoalsFolder: "23 Goals",
	}
	ops, ok := cleanupOpsFor(resolved).(*cleanupVaultOps)
	Expect(ok).To(BeTrue())
	return *ops, dir
}

func writeVaultFile(dir, rel, content string) {
	path := filepath.Join(dir, rel)
	Expect(os.MkdirAll(filepath.Dir(path), 0o750)).To(Succeed())
	Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
}

func vaultFileContent(dir, rel string) string {
	data, err := os.ReadFile(filepath.Join(dir, rel))
	Expect(err).NotTo(HaveOccurred())
	return string(data)
}

var _ = Describe("cleanup VaultOps adapter", func() {
	const taskFile = "24 Tasks/Task A.md"
	const goalFile = "23 Goals/Goal A.md"

	It("lists the on-disk tasks and goals", func() {
		ops, _ := cleanupAdapterFixture()

		tasks, err := ops.ListTasks(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(tasks).To(HaveLen(1))
		Expect(tasks[0].ID).To(Equal("Task A"))
		Expect(tasks[0].Title).To(Equal("Task A"))
		Expect(tasks[0].Status).To(Equal("next"))

		goals, err := ops.ListGoals(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(goals).To(HaveLen(1))
		Expect(goals[0].ID).To(Equal("Goal A"))
	})

	It("shows a task by id", func() {
		ops, _ := cleanupAdapterFixture()

		task, err := ops.ShowTask(context.Background(), "Task A")
		Expect(err).NotTo(HaveOccurred())
		Expect(task.ID).To(Equal("Task A"))
		Expect(task.Assignee).To(Equal("alice"))
	})

	It("writes a task field into the task's own file", func() {
		ops, dir := cleanupAdapterFixture()

		Expect(ops.SetTaskField(
			context.Background(), "Task A", "claude_session_started", "MARKER",
		)).To(Succeed())
		Expect(vaultFileContent(dir, taskFile)).To(ContainSubstring("claude_session_started"))
		Expect(vaultFileContent(dir, taskFile)).To(ContainSubstring("MARKER"))
		Expect(vaultFileContent(dir, goalFile)).NotTo(ContainSubstring("MARKER"))

		Expect(ops.ClearTaskField(
			context.Background(), "Task A", "claude_session_started",
		)).To(Succeed())
		Expect(vaultFileContent(dir, taskFile)).NotTo(ContainSubstring("claude_session_started"))
	})

	It("writes a goal field into the goal's own file", func() {
		ops, dir := cleanupAdapterFixture()

		Expect(ops.SetGoalField(
			context.Background(), "Goal A", "claude_session_started", "MARKER",
		)).To(Succeed())
		Expect(vaultFileContent(dir, goalFile)).To(ContainSubstring("claude_session_started"))
		Expect(vaultFileContent(dir, goalFile)).To(ContainSubstring("MARKER"))
		Expect(vaultFileContent(dir, taskFile)).NotTo(ContainSubstring("MARKER"))

		Expect(ops.ClearGoalField(
			context.Background(), "Goal A", "claude_session_started",
		)).To(Succeed())
		Expect(vaultFileContent(dir, goalFile)).NotTo(ContainSubstring("claude_session_started"))
	})
})
