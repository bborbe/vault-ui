// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "embed"

// frontendDist carries the Vite build into the binary, so the served page and
// the shipped assets are always the same bytes. `make build` runs the frontend
// build first; frontend/dist is git-ignored except for its .gitkeep, which keeps
// the directory present so this directive compiles on a fresh clone.
//
//go:embed all:frontend/dist
var frontendDist embed.FS
