// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/mocks"
	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/factory"
)

const (
	taskFile  = "Tasks/Task A.md"
	goalFile  = "Goals/Goal A.md"
	topicFile = "23 Topics/Topic A.md"
)

// tempVault creates a temp vault fixture with the tasks, goals, and topics
// directories the ops are driven against, and returns its root path.
func tempVault() string {
	vaultDir := tempDir()
	for _, dir := range []string{"Tasks", "Goals", "23 Topics", "Daily Notes"} {
		Expect(os.MkdirAll(filepath.Join(vaultDir, dir), 0750)).To(Succeed())
	}
	writeFile(vaultDir, taskFile, "---\nstatus: next\npage_type: task\n---\n# Task A\n\nA task.\n")
	writeFile(vaultDir, goalFile, "---\nstatus: active\npage_type: goal\n---\n# Goal A\n\nA goal.\n")
	writeFile(vaultDir, topicFile, "---\npage_type: topic\n---\n# Topic A\n\nA topic.\n")
	return vaultDir
}

// writeFile writes content to a vault-relative path.
func writeFile(vaultDir, relPath, content string) {
	Expect(os.WriteFile(filepath.Join(vaultDir, relPath), []byte(content), 0600)).
		To(Succeed())
}

// frontmatterValue reads a vault file and returns the value of key in its
// leading "---"-delimited frontmatter block, or "" when the key is absent.
func frontmatterValue(vaultDir, relPath, key string) string {
	data, err := os.ReadFile(filepath.Join(vaultDir, relPath))
	Expect(err).NotTo(HaveOccurred())
	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		return ""
	}
	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return ""
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == key {
			return strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		}
	}
	return ""
}

// snapshotVault returns the bytes of every file under dir, keyed by path.
func snapshotVault(dir string) map[string]string {
	snapshot := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot[path] = string(data)
		return nil
	})
	Expect(err).NotTo(HaveOccurred())
	return snapshot
}

// createOpSet builds the real op set from the fixture vault, with fakes for the
// session-spawning and publishing dependencies.
func createOpSet(vaultDir string) vaultui.OpSet {
	vault := mustVault(vaultDir)

	starter := &mocks.ClaudeSessionStarter{}
	starter.StartSessionReturns(nil)
	resumer := &mocks.ClaudeResumer{}
	resumer.ResumeSessionReturns(nil)
	counter := &mocks.InteractionCounter{}
	counter.CountReturns(0)
	publisher := ops.NewEscalationPublisher("", "", ops.NewKafkaNotificationSenderFactory())
	return factory.CreateOpSet(
		vault,
		libtime.NewCurrentDateTime(),
		publisher,
		starter,
		resumer,
		counter,
		uuid.NewString,
		func(string) (ops.ClaudeSessionStarter, ops.ClaudeResumer) {
			return starter, resumer
		},
	)
}

var _ = Describe("CreateOpSet", func() {
	It("populates every operation", func() {
		set := createOpSet(tempVault())

		Expect(set.List).NotTo(BeNil())
		Expect(set.Show).NotTo(BeNil())
		Expect(set.FrontmatterSet).NotTo(BeNil())
		Expect(set.FrontmatterClear).NotTo(BeNil())
		Expect(set.WorkOn).NotTo(BeNil())
		Expect(set.Approve).NotTo(BeNil())
		Expect(set.Answer).NotTo(BeNil())
		Expect(set.Defer).NotTo(BeNil())
		Expect(set.Complete).NotTo(BeNil())
		Expect(set.GoalSet).NotTo(BeNil())
		Expect(set.GoalClear).NotTo(BeNil())
		Expect(set.GoalWorkOn).NotTo(BeNil())
		Expect(set.GoalDefer).NotTo(BeNil())
		Expect(set.GoalComplete).NotTo(BeNil())
		Expect(set.TopicShow).NotTo(BeNil())
		Expect(set.TopicSet).NotTo(BeNil())
		Expect(set.TopicClear).NotTo(BeNil())
	})

	Context("read ops against the temp vault", func() {
		It("lists the task under the tasks dir", func() {
			vaultDir := tempVault()
			set := createOpSet(vaultDir)

			items, err := set.List.Execute(ctx, vaultDir, "test", "Tasks", nil, true, "", "")

			Expect(err).NotTo(HaveOccurred())
			Expect(items).To(HaveLen(1))
			Expect(items[0].Name).To(Equal("Task A"))
			Expect(items[0].Status).To(Equal("next"))
		})

		It("shows one task's frontmatter", func() {
			vaultDir := tempVault()
			set := createOpSet(vaultDir)

			detail, err := set.Show.Execute(ctx, vaultDir, "test", "Task A")

			Expect(err).NotTo(HaveOccurred())
			Expect(detail.Name).To(Equal("Task A"))
			Expect(detail.Status).To(Equal("next"))
		})

		It("shows a topic", func() {
			vaultDir := tempVault()
			set := createOpSet(vaultDir)

			result, err := set.TopicShow.Execute(ctx, vaultDir, "test", "Topic A")

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Name).To(Equal("Topic A"))
		})
	})

	Context("goal and topic listing reuse the generic list op", func() {
		It("returns two distinct result sets for two directories", func() {
			vaultDir := tempVault()
			set := createOpSet(vaultDir)

			goals, err := set.List.Execute(ctx, vaultDir, "test", "Goals", nil, true, "", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(goals).To(HaveLen(1))
			Expect(goals[0].Name).To(Equal("Goal A"))

			topics, err := set.List.Execute(ctx, vaultDir, "test", "23 Topics", nil, true, "", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(topics).To(HaveLen(1))
			Expect(topics[0].Name).To(Equal("Topic A"))

			Expect(goals[0].Name).NotTo(Equal(topics[0].Name))
		})
	})

	Context("write ops produce the expected frontmatter", func() {
		It("sets and clears a task frontmatter field", func() {
			vaultDir := tempVault()
			set := createOpSet(vaultDir)

			Expect(set.FrontmatterSet.Execute(
				ctx, vaultDir, "Task A", "priority", "5", "", "", "tester", false,
			)).To(Succeed())
			Expect(frontmatterValue(vaultDir, taskFile, "priority")).To(Equal("5"))

			Expect(set.FrontmatterClear.Execute(
				ctx, vaultDir, "Task A", "priority",
			)).To(Succeed())
			Expect(frontmatterValue(vaultDir, taskFile, "priority")).To(BeEmpty())
		})

		It("work-on sets the task session field", func() {
			vaultDir := tempVault()
			set := createOpSet(vaultDir)

			result, err := set.WorkOn.Execute(
				ctx, vaultDir, "Task A", "alice", "test", false, vaultDir,
				mustVault(vaultDir),
			)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Success).To(BeTrue())
			Expect(frontmatterValue(vaultDir, taskFile, "claude_session_id")).
				NotTo(BeEmpty())
		})

		It("builds the work-on starter through the launcher factory for a resolved launcher", func() {
			vaultDir := tempVault()
			writeFile(vaultDir, "Tasks/Launcher Task.md",
				"---\nstatus: in_progress\npage_type: task\nlauncher: cc-private-claude\n---\n"+
					"# Launcher Task\n\nA task.\n")

			vault := mustVault(vaultDir)
			starter := &mocks.ClaudeSessionStarter{}
			starter.StartSessionReturns(nil)
			resumer := &mocks.ClaudeResumer{}
			resumer.ResumeSessionReturns(nil)
			counter := &mocks.InteractionCounter{}
			counter.CountReturns(0)
			gotScript := ""
			set := factory.CreateOpSet(
				vault,
				libtime.NewCurrentDateTime(),
				ops.NewEscalationPublisher("", "", ops.NewKafkaNotificationSenderFactory()),
				starter,
				resumer,
				counter,
				uuid.NewString,
				func(script string) (ops.ClaudeSessionStarter, ops.ClaudeResumer) {
					gotScript = script
					return starter, resumer
				},
			)

			result, err := set.WorkOn.Execute(
				ctx, vaultDir, "Launcher Task", "alice", "test", false, vaultDir, vault,
			)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Success).To(BeTrue())
			Expect(gotScript).To(Equal("cc-private-claude"))
		})

		It("defer sets the task defer_date", func() {
			vaultDir := tempVault()
			set := createOpSet(vaultDir)

			_, err := set.Defer.Execute(ctx, vaultDir, "Task A", "2026-12-31", "test")

			Expect(err).NotTo(HaveOccurred())
			Expect(frontmatterValue(vaultDir, taskFile, "defer_date")).
				To(HavePrefix("2026-12-31"))
		})

		It("complete sets the task status to completed", func() {
			vaultDir := tempVault()
			set := createOpSet(vaultDir)

			_, err := set.Complete.Execute(ctx, vaultDir, "Task A", "test", false, "", "")

			Expect(err).NotTo(HaveOccurred())
			Expect(frontmatterValue(vaultDir, taskFile, "status")).To(Equal("completed"))
		})

		It("goal work-on sets the goal session field", func() {
			vaultDir := tempVault()
			set := createOpSet(vaultDir)

			_, err := set.GoalWorkOn.Execute(
				ctx, vaultDir, "Goal A", "alice", "test", false, vaultDir,
				mustVault(vaultDir),
			)

			Expect(err).NotTo(HaveOccurred())
			Expect(frontmatterValue(vaultDir, goalFile, "claude_session_id")).
				NotTo(BeEmpty())
		})

		It("goal defer sets the goal defer_date", func() {
			vaultDir := tempVault()
			set := createOpSet(vaultDir)

			_, err := set.GoalDefer.Execute(ctx, vaultDir, "Goal A", "2026-12-31", "test")

			Expect(err).NotTo(HaveOccurred())
			Expect(frontmatterValue(vaultDir, goalFile, "defer_date")).
				To(HavePrefix("2026-12-31"))
		})

		It("goal complete sets the goal status to completed", func() {
			vaultDir := tempVault()
			set := createOpSet(vaultDir)

			_, err := set.GoalComplete.Execute(ctx, vaultDir, "Goal A", "test", false, "", "")

			Expect(err).NotTo(HaveOccurred())
			Expect(frontmatterValue(vaultDir, goalFile, "status")).To(Equal("completed"))
		})

		It("goal set and clear a frontmatter field", func() {
			vaultDir := tempVault()
			set := createOpSet(vaultDir)

			Expect(set.GoalSet.Execute(
				ctx, vaultDir, "Goal A", "assignee", "alice", "", "",
			)).To(Succeed())
			Expect(frontmatterValue(vaultDir, goalFile, "assignee")).To(Equal("alice"))

			Expect(set.GoalClear.Execute(
				ctx, vaultDir, "Goal A", "assignee",
			)).To(Succeed())
			Expect(frontmatterValue(vaultDir, goalFile, "assignee")).To(BeEmpty())
		})

		It("topic set and clear a frontmatter field", func() {
			vaultDir := tempVault()
			set := createOpSet(vaultDir)

			Expect(set.TopicSet.Execute(
				ctx, vaultDir, "Topic A", "assignee", "alice", "", "",
			)).To(Succeed())
			Expect(frontmatterValue(vaultDir, topicFile, "assignee")).To(Equal("alice"))

			Expect(set.TopicClear.Execute(
				ctx, vaultDir, "Topic A", "assignee",
			)).To(Succeed())
			Expect(frontmatterValue(vaultDir, topicFile, "assignee")).To(BeEmpty())
		})
	})

	Context("write-op errors surface", func() {
		It("returns an error and leaves the vault untouched for a missing entity", func() {
			vaultDir := tempVault()
			set := createOpSet(vaultDir)
			before := snapshotVault(vaultDir)

			_, err := set.Complete.Execute(
				ctx, vaultDir, "Does Not Exist", "test", false, "", "",
			)

			Expect(err).To(HaveOccurred())
			Expect(snapshotVault(vaultDir)).To(Equal(before))
			_, statErr := os.Stat(filepath.Join(vaultDir, "Tasks", "Does Not Exist.md"))
			Expect(os.IsNotExist(statErr)).To(BeTrue())
		})
	})
})

// mustVault resolves the fixture vault through the config loader.
func mustVault(vaultDir string) *config.Vault {
	loader := factory.CreateConfigLoader(writeVaultConfig(vaultDir))
	vault, err := loader.GetVault(ctx, "test")
	Expect(err).NotTo(HaveOccurred())
	return vault
}
