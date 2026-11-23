// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mutations

import (
	"context"

	"github.com/bborbe/vault-ui/pkg/api"
	"github.com/bborbe/vault-ui/pkg/pageindex"
	"github.com/bborbe/vault-ui/pkg/vaultconfig"
)

// topicNotFound is the 404 text for a missing topic on the session routes,
// matching the read side's TopicNotFoundError.
func topicNotFound(topicID string) string { return "Topic not found: " + topicID }

// RunTopic starts a Claude session for a topic, mirroring RunGoal.
func (s *service) RunTopic(ctx context.Context, vault, topicID string) (api.SessionResponse, error) {
	if guard := requireSafeID(topicID, "topic_id"); guard != nil {
		return api.SessionResponse{}, guard
	}
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return api.SessionResponse{}, err
	}
	if !ok {
		return api.SessionResponse{}, newHTTPError(500, unknownVault(vault))
	}
	topic, found, err := s.findTopic(ctx, resolved, topicID)
	if err != nil {
		return api.SessionResponse{}, newHTTPError(500, err.Error())
	}
	if !found {
		return api.SessionResponse{}, newHTTPError(404, topicNotFound(topicID))
	}
	defer s.markTopicDirty(resolved)
	set := s.opsForVault(resolved)
	s.deps.Launch.Begin(vault, topicID, kindTopic)
	if err := set.TopicSet.Execute(
		ctx, resolved.Path, topicID, "claude_session_started", sessionStartedMarker(), "", "", "",
	); err != nil {
		s.deps.Launch.Finish(vault, topicID)
		return api.SessionResponse{}, newHTTPError(500, err.Error())
	}
	result, workErr := set.TopicWorkOn.Execute(
		ctx, resolved.Path, topicID, "", resolved.Name, false, resolved.SessionProjectDir,
		cliVault(resolved),
	)
	if workErr != nil {
		_ = set.TopicClear.Execute(ctx, resolved.Path, topicID, "claude_session_started")
		s.deps.Launch.Finish(vault, topicID)
		if s.deps.Launch.WasTakenOver(vault, topicID) {
			return api.SessionResponse{}, newHTTPError(
				409,
				"Launch ended by take-over from the wall — "+
					"resume the session from the take-over modal",
			)
		}
		return api.SessionResponse{}, newHTTPError(500, workErr.Error())
	}
	s.deps.Launch.Finish(vault, topicID)
	if setErr := set.TopicSet.Execute(
		ctx, resolved.Path, topicID, "claude_session_id", result.SessionID, "", "", "",
	); setErr != nil {
		return api.SessionResponse{}, newHTTPError(500, setErr.Error())
	}
	_ = set.TopicClear.Execute(ctx, resolved.Path, topicID, "claude_session_started")
	s.deps.Cache.Invalidate(vault, topicID)
	if result.SessionID == "" {
		return api.SessionResponse{}, newHTTPError(
			500, "vault-cli topic work-on did not start a claude session: no warnings reported",
		)
	}
	return sessionResponse(resolved, resolved.ClaudeScript, result.SessionID, topic.Name), nil
}

// JumpTopic activates the WezTerm pane a live topic's manager session runs in,
// mirroring JumpTask. sameOrigin carries the handler's same-origin verdict.
func (s *service) JumpTopic(ctx context.Context, vault, topicID string, sameOrigin bool) error {
	if !sameOrigin {
		return newHTTPError(403, "cross-origin jump request rejected")
	}
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return err
	}
	if !ok {
		return newHTTPError(500, unknownVault(vault))
	}
	topic, found, err := s.findTopic(ctx, resolved, topicID)
	if err != nil {
		return newHTTPError(500, err.Error())
	}
	if !found {
		return newHTTPError(404, topicNotFound(topicID))
	}
	return s.jumpToSession(ctx, topic.ClaudeSessionID)
}

// TakeOverTopic terminates a live or starting topic session and returns the
// resume command, mirroring TakeOverGoal.
func (s *service) TakeOverTopic(
	ctx context.Context, vault, topicID string,
) (api.SessionResponse, error) {
	if guard := requireSafeID(topicID, "topic_id"); guard != nil {
		return api.SessionResponse{}, guard
	}
	resolved, ok, err := s.vaultByName(ctx, vault)
	if err != nil {
		return api.SessionResponse{}, err
	}
	if !ok {
		return api.SessionResponse{}, newHTTPError(500, unknownVault(vault))
	}
	topic, found, err := s.findTopic(ctx, resolved, topicID)
	if err != nil {
		return api.SessionResponse{}, newHTTPError(500, err.Error())
	}
	if !found {
		return api.SessionResponse{}, newHTTPError(404, topicNotFound(topicID))
	}
	defer s.markTopicDirty(resolved)
	set := s.opsForVault(resolved)
	sessionID := topic.ClaudeSessionID
	terminated := false
	// The status cache indexes Themes/Objectives/Goals/Tasks only, so a topic's
	// claude_session_started is never in it and startingMarker would fall through
	// to an empty file fallback on every take-over — routing an in-flight launch
	// down the resume branch. Read the page's own marker instead.
	fileMarker := ""
	if show, showErr := set.TopicShow.Execute(
		ctx, resolved.Path, resolved.Name, topicID,
	); showErr == nil {
		fileMarker = show.Fields["claude_session_started"]
	}
	if s.startingMarker(vault, topicID, fileMarker) != "" {
		var resolvedID string
		resolvedID, terminated = s.terminateLaunch(ctx, sessionID, topic.Name)
		if resolvedID != "" {
			sessionID = resolvedID
		}
		if err := s.clearStartingMarker(ctx, resolved, set, vault, topicID, kindTopic); err != nil {
			return api.SessionResponse{}, err
		}
		if sessionID != "" {
			s.bindSessionID(ctx, resolved, set, vault, topicID, kindTopic, sessionID)
		}
		if sessionID == "" {
			return api.SessionResponse{}, newHTTPError(
				400,
				"Topic has no Claude session to resume: "+topicID+
					" (launch marker cleared — the card is back to Start)",
			)
		}
	} else {
		if sessionID == "" {
			return api.SessionResponse{}, newHTTPError(
				400, "Topic has no Claude session to take over: "+topicID,
			)
		}
		terminated = s.terminateResumed(ctx, sessionID)
	}
	response := sessionResponse(resolved, resolved.ClaudeScript, sessionID, topic.Name)
	response.Terminated = &terminated
	return response, nil
}

// topicWritten is taskWritten and goalWritten for a publishing topic route:
// status cache, per-file page-index dirty mark, then the topic update frame.
func (s *service) topicWritten(
	vault vaultconfig.Vault, topicID string,
) func(context.Context) {
	return func(ctx context.Context) {
		s.deps.Cache.Invalidate(vault.Name, topicID)
		s.deps.Index.MarkFileDirty(
			pageindex.NewKey(vault.Path, vault.TopicsFolder), topicID,
		)
		s.deps.Publisher.PublishTopicUpdated(ctx, vault.Name, topicID)
	}
}
