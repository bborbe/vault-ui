// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package staticui embeds the frozen frontend tree so the Go backend serves it
// byte-for-byte. The embed source is src/vault_ui/static/, which is unchanged
// by the Go rewrite; this file lives beside it (not inside it) so the static
// tree itself stays untouched.
package staticui

import "embed"

// FS is the frozen frontend tree: index.html, app.js, style.css.
//
//go:embed static
var FS embed.FS
