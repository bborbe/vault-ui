# staticui — the embedded frontend tree

This directory holds two things and nothing else:

- `static/` — `index.html`, `app.js`, `style.css`. The frozen frontend. The Go
  binary embeds this tree and serves it byte-for-byte, so a change here ships
  with the binary.
- `static_embed.go` — package `staticui`, the `//go:embed static` shim the Go
  backend imports.

The Python backend that used to live here was removed; the Go backend
([`main.go`](../../main.go), [`pkg/`](../../pkg/)) is the only implementation.
