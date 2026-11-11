// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board

import (
	"context"
	stderrors "errors"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bborbe/errors"
	"github.com/bborbe/vault-cli/pkg/storage"
	"github.com/golang/glog"

	"github.com/bborbe/vault-ui/pkg/activity"
	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/cleanup"
	"github.com/bborbe/vault-ui/pkg/session"
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
func (b *board) ListTopics(ctx context.Context, query TopicQuery) ([]api.TopicResponse, error) {
	all, err := b.vaults.Vaults(ctx)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "list vaults")
	}
	selected := b.selectVaults(all, query.Vaults)

	registryIDs := b.signals.RegistrySessionIDs(ctx)
	seenIDs := b.signals.SeenSessionIDs(ctx)
	liveIDsUnknown := !b.signals.LiveIDsKnown()
	resumeIDs := b.signals.ResumeSessionIDs(ctx)

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

		projectDir := cleanup.DeriveClaudeProjectDir(b.homeDir, vault.Path, vault.SessionProjectDir)
		projectsRoot := filepath.Join(b.homeDir, ".claude", "projects")
		params := session.ClassifyParams{
			ProjectDir:         projectDir,
			ProjectsRoot:       projectsRoot,
			Now:                b.clock.Now().UTC(),
			LiveWindow:         session.DefaultLiveWindow,
			ResumeSessionIDs:   resumeIDs,
			RegistrySessionIDs: registryIDs,
			SeenSessionIDs:     seenIDs,
			LiveIDsUnknown:     liveIDsUnknown,
			TranscriptMtime:    b.transcriptProbe(),
		}

		// One ListPages for the whole vault, exactly as the task path reads its
		// page-derived fields: the topic's `jira:` value is not on the list item.
		pageJiraByTopic := b.topicPageFieldsByTopic(ctx, vault)

		for _, item := range items {
			blockers := b.uncompletedBlockers(vault.Name, item.BlockedBy)
			responses = append(responses, api.TopicResponse{
				ID:           item.Name,
				Title:        item.Name,
				Status:       item.Status,
				Phase:        strPtr(item.Phase),
				SessionState: sessionStatePtr(classifySessionID(ctx, item.ClaudeSessionID, params)),
				Flag:         item.Flag,
				Vault:        vault.Name,
				ObsidianURL:  topicURL(vault, item.Name),
				Assignee:     strPtr(item.Assignee),
				Priority:     priorityValue(item.Priority),
				ActivityDate: dateTimeString(activity.ComputeActivityDateWith(
					ctx,
					b.transcriptProbe(),
					parseDateTime(item.ModifiedDate),
					item.ClaudeSessionID,
					projectDir,
					projectsRoot,
				)),
				Jira:     pageJiraByTopic[item.Name],
				Blockers: blockers,
				Blocked:  len(blockers) > 0,
			})
		}
	}

	if query.SessionLive {
		live := make([]api.TopicResponse, 0, len(responses))
		for _, response := range responses {
			if response.SessionState != nil && *response.SessionState == string(session.SessionStateLive) {
				live = append(live, response)
			}
		}
		responses = live
	}
	return responses, nil
}

// topicPageFieldsByTopic returns each topic page's `jira:` frontmatter value,
// keyed by the page's file name. It is one ListPages per vault, the same
// snapshot the topic list already reads, so it adds no walk of its own.
//
// It is deliberately separate from taskPageFieldsByTask: that helper lists the
// tasks folder and parses Open Questions, neither of which applies to a topic.
// A failure is not fatal to a board read: it is logged and every topic carries
// a nil Jira rather than the whole topic list failing.
func (b *board) topicPageFieldsByTopic(
	ctx context.Context, vault Vault,
) map[string]*string {
	byName := map[string]*string{}
	if b.pageIndex == nil {
		// A board built without a page index carries no page-derived fields, on
		// the same terms as the task helper: degrade, never fail the read.
		return byName
	}
	pages, err := b.pageIndex.ListPages(ctx, vault.Path, vault.TopicsFolder)
	if err != nil {
		glog.Warningf("list pages for topic fields in vault %s: %v", vault.Name, err)
		return byName
	}
	for _, page := range pages {
		select {
		case <-ctx.Done():
			// The caller is gone: stop rather than walk the remaining pages.
			return byName
		default:
		}
		byName[page.FileMetadata.Name] = pageJira(page)
	}
	return byName
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
