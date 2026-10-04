// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board

import (
	"context"
	stderrors "errors"
	"regexp"
	"strings"

	"github.com/bborbe/errors"
	"github.com/bborbe/vault-cli/pkg/storage"

	"github.com/bborbe/vault-ui/pkg/api"
)

// topicSectionHeading is the heading whose bullets carry a topic's tracked work.
const topicSectionHeading = "## Goals"

// bulletWikilinkRe is the leading wikilink of a bullet, alias stripped.
var bulletWikilinkRe = regexp.MustCompile(`^\[\[([^\]|]+)(?:\|[^\]]*)?\]\]`)

// TopicNotFoundError reports a topic id the vault cannot resolve.
type TopicNotFoundError struct {
	TopicID string
}

func (e TopicNotFoundError) Error() string {
	return "Topic not found: " + e.TopicID
}

// ListTopics reproduces GET /api/topics.
func (b *board) ListTopics(ctx context.Context, vaults []string) ([]api.TopicResponse, error) {
	all, err := b.vaults.Vaults(ctx)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "list vaults")
	}
	selected := b.selectVaults(all, vaults)

	responses := make([]api.TopicResponse, 0, len(selected))
	for _, vault := range selected {
		if vault.TopicsFolder == "" {
			continue
		}
		items, listErr := b.ops.List(vault).Execute(
			ctx, vault.Path, vault.Name, vault.TopicsFolder, nil, true, "", "",
		)
		if listErr != nil {
			return nil, errors.Wrapf(ctx, listErr, "list topics for vault %s", vault.Name)
		}
		for _, item := range items {
			responses = append(responses, api.TopicResponse{
				ID:          item.Name,
				Title:       item.Name,
				Status:      item.Status,
				Vault:       vault.Name,
				ObsidianURL: topicURL(vault, item.Name),
			})
		}
	}
	return responses, nil
}

// ShowTopic reproduces GET /api/topics/{topic_id}.
func (b *board) ShowTopic(
	ctx context.Context,
	vaultName, topicID string,
) (api.TopicDetailResponse, error) {
	all, err := b.vaults.Vaults(ctx)
	if err != nil {
		return api.TopicDetailResponse{}, errors.Wrap(ctx, err, "list vaults")
	}
	vault, ok := b.findVault(all, vaultName)
	if !ok {
		return api.TopicDetailResponse{}, UnknownVaultError{Vault: vaultName}
	}

	topic, err := b.ops.TopicShow(vault).Execute(ctx, vault.Path, vault.Name, topicID)
	if err != nil {
		if stderrors.Is(err, storage.ErrNotFound) {
			return api.TopicDetailResponse{}, TopicNotFoundError{TopicID: topicID}
		}
		return api.TopicDetailResponse{}, errors.Wrapf(ctx, err, "show topic %s", topicID)
	}

	goalItems, err := b.ops.List(vault).Execute(
		ctx, vault.Path, vault.Name, vault.GoalsFolder, nil, true, "", "",
	)
	if err != nil {
		return api.TopicDetailResponse{}, errors.Wrapf(ctx, err, "list goals for vault %s", vault.Name)
	}
	taskItems, err := b.ops.List(vault).Execute(
		ctx, vault.Path, vault.Name, vault.TasksFolder, nil, true, "", "",
	)
	if err != nil {
		return api.TopicDetailResponse{}, errors.Wrapf(ctx, err, "list tasks for vault %s", vault.Name)
	}

	goalNames := make(map[string]bool, len(goalItems))
	for _, item := range goalItems {
		goalNames[item.Name] = true
	}
	taskNames := make(map[string]bool, len(taskItems))
	for _, item := range taskItems {
		taskNames[item.Name] = true
	}

	goals, tasks, unresolved := classifyTopicEntries(topic.Content, goalNames, taskNames)

	status := topic.Fields["status"]
	if status == "" {
		status = "unknown"
	}

	return api.TopicDetailResponse{
		ID:          topic.Name,
		Title:       topic.Name,
		Status:      status,
		Vault:       vault.Name,
		ObsidianURL: topicURL(vault, topic.Name),
		Goals:       goals,
		Tasks:       tasks,
		Unresolved:  unresolved,
	}, nil
}

// topicURL builds the Obsidian deep link for a topic page.
func topicURL(vault Vault, topicID string) string {
	filePath := topicID + ".md"
	if vault.TopicsFolder != "" {
		filePath = vault.TopicsFolder + "/" + topicID + ".md"
	}
	return obsidianURL(vault.VaultName, filePath)
}

// parseTopicEntries returns the entry names of a topic page's `## Goals`
// section, mirroring Python `_parse_topic_entries`.
func parseTopicEntries(content string) []string {
	entries := []string{}
	inSection := false
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "## ") {
			if inSection {
				break
			}
			inSection = strings.TrimSpace(line) == topicSectionHeading
			continue
		}
		if !inSection || !strings.HasPrefix(line, "- ") {
			continue
		}
		match := bulletWikilinkRe.FindStringSubmatch(strings.TrimSpace(line[2:]))
		if match != nil {
			entries = append(entries, strings.TrimSpace(match[1]))
		}
	}
	return entries
}

// classifyTopicEntries splits a topic's entries into (goals, tasks, unresolved).
func classifyTopicEntries(
	content string,
	goalNames, taskNames map[string]bool,
) ([]string, []string, []string) {
	goals := []string{}
	tasks := []string{}
	unresolved := []string{}
	for _, name := range parseTopicEntries(content) {
		switch {
		case goalNames[name]:
			goals = append(goals, name)
		case taskNames[name]:
			tasks = append(tasks, name)
		default:
			unresolved = append(unresolved, name)
		}
	}
	return goals, tasks, unresolved
}
