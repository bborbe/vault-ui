---
status: idea
---

## Idea

Two configured vaults can resolve to the same real directory (`~/Documents/Obsidian/OpenClaw` is a symlink to `private-agent`, 10,713 task files). The page index keys entries by the configured path, so the folder is indexed twice: double cold-build cost, double memory, and one extra single-file read per watcher event after incremental updates land.

Share one index entry per resolved real directory, while keeping each vault's own `FilePath`/vault path in the pages it serves. Split out of `incremental-page-index-updates` (audit M2/M3): it is a fixed memory/build cost, not one that scales with change size, and it carries its own risks (entry identity, `FilePath` carrying the wrong vault path, symlink retarget at runtime).
