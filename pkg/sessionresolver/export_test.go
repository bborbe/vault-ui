// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sessionresolver

// ResetCacheForTest drops every cached transcript entry so the next resolve is
// computed as a fresh process would compute it. Test-only.
func ResetCacheForTest() {
	transcriptCacheMu.Lock()
	defer transcriptCacheMu.Unlock()
	transcriptCache = map[string]*cacheEntry{}
}
