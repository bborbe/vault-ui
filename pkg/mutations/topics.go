// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mutations

import (
	"context"

	"github.com/bborbe/vault-ui/pkg/pageindex"
	"github.com/bborbe/vault-ui/pkg/vaultconfig"
)

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
