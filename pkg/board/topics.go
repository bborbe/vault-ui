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
		pageFieldsByTopic := b.topicPageFieldsByTopic(vault)

		for _, item := range items {
			blockers := b.uncompletedBlockers(b.taskStatuses(vault.VaultName), item.BlockedBy)
			state := classifySessionID(ctx, item.ClaudeSessionID, params)
			var location *string
			if state == session.SessionStateLive {
				location = liveLocationPtr(state, b.signals.RegistryLocation(ctx, item.ClaudeSessionID))
			}
			responses = append(responses, api.TopicResponse{
				ID:                   item.Name,
				Title:                item.Name,
				Status:               item.Status,
				Phase:                strPtr(item.Phase),
				SessionState:         sessionStatePtr(state),
				Location:             location,
				ClaudeSessionID:      strPtr(item.ClaudeSessionID),
				ClaudeSessionStarted: pageFieldsByTopic[item.Name].started,
				Flag:                 item.Flag,
				Vault:                vault.Name,
				ObsidianURL:          topicURL(vault, item.Name),
				Assignee:             strPtr(item.Assignee),
				Priority:             priorityValue(item.Priority),
				ActivityDate: dateTimeString(activity.ComputeActivityDateWith(
					ctx,
					b.transcriptProbe(),
					parseDateTime(item.ModifiedDate),
					item.ClaudeSessionID,
					projectDir,
					projectsRoot,
				)),
				Jira:     pageFieldsByTopic[item.Name].jira,
				Mode:     pageFieldsByTopic[item.Name].mode,
				Blockers: blockers,
				Blocked:  len(blockers) > 0,
			})
		}
	}

	b.attachTopicJumpPanes(ctx, responses)

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

// attachTopicJumpPanes resolves the panes of the live topic rows in one batch and
// stamps each live row's JumpPane, mirroring attachGoalJumpPanes. A nil Panes
// dependency leaves every row's JumpPane nil, which is the feature-inert
// configuration. Only live rows are resolved: a headless manager is live but has
// no terminal.
func (b *board) attachTopicJumpPanes(ctx context.Context, responses []api.TopicResponse) {
	if b.panes == nil {
		return
	}
	seen := make(map[string]bool, len(responses))
	sessionIDs := make([]string, 0, len(responses))
	for i := range responses {
		sessionID := topicSessionID(responses[i])
		if !topicRowLive(responses[i]) || sessionID == "" || seen[sessionID] {
			continue
		}
		seen[sessionID] = true
		sessionIDs = append(sessionIDs, sessionID)
	}
	if len(sessionIDs) == 0 {
		return
	}
	panes := b.panes.ResolveAll(ctx, sessionIDs)
	for i := range responses {
		if !topicRowLive(responses[i]) {
			continue
		}
		if paneID, ok := panes[topicSessionID(responses[i])]; ok {
			value := paneID
			responses[i].JumpPane = &value
		}
	}
}

// topicRowLive reports whether the response's classified session state is live.
func topicRowLive(response api.TopicResponse) bool {
	return response.SessionState != nil && *response.SessionState == string(session.SessionStateLive)
}

// topicSessionID returns the response's claude session id, or "" when absent.
func topicSessionID(response api.TopicResponse) string {
	if response.ClaudeSessionID == nil {
		return ""
	}
	return *response.ClaudeSessionID
}

// pageKeyFields is the per-page state the topic and goal boards derive from a page
// that their list rows do not carry: the page's `jira:` and `mode:` frontmatter
// values. The goal board reads only `mode` — a goal payload carries no jira key.
// Named pageKeyFields, not pageFields: tasks.go declares a local `pageFields`
// variable, and a package-scope type of that name would shadow it.
type pageKeyFields struct {
	jira    *string
	mode    *string
	started *string
}

// topicPageFieldsByTopic returns each topic's `mode:` value from the vault-api
// source, keyed by the page name. The vault-api topic item carries `mode`; it
// carries no jira or started marker, and no topic page in the vault declares
// them, so those fields stay nil.
func (b *board) topicPageFieldsByTopic(vault Vault) map[string]pageKeyFields {
	byName := map[string]pageKeyFields{}
	if b.source == nil {
		return byName
	}
	for _, t := range b.source.Topics(vault.VaultName) {
		byName[t.Title] = pageKeyFields{mode: stringPtrOrNil(t.Mode)}
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
