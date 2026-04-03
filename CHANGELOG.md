# Changelog

All notable changes to this project are documented in this file.

The format follows Keep a Changelog and uses semantic version tags (`vMAJOR.MINOR.PATCH`).

## [Unreleased]

### Changed
- `/chat` now preserves streamed assistant bubble timestamps across normalized multi-part replies, preventing commentary bubbles from collapsing to the end of the timeline beneath terminal activity.
- `/chat` live streaming now keeps source-identified assistant commentary in separate bubbles instead of merging it into a generic pending assistant placeholder.
- `/chat` now normalizes assistant source identity across delta/completion events and collapses adjacent duplicate assistant commentary bubbles in the final timeline.
- `/chat` subagent wait-completion bubbles now carry the known subagent identity when available, rendering labels like `Finished waiting for Planck.` instead of a generic completion row.
- Chat session persistence now keeps only the active chat-only schema, prunes the unused `usage_snapshots` table, and no longer relies on `runtime_*` session columns.
- Account autoswitch now retries additional backup accounts when the best candidate cannot be activated, and autoswitch refresh failures log account emails instead of opaque `codex_*` ids when available.
- Coding template/runtime skill bootstrap now uses only the configured `superpowers` repository and no longer falls back to bundled local `codex-skills` assets.
- `/chat` now shows explicit retry/recovery progress in the status line during account-switch and runtime-restart recovery flows.
- `/chat` is a single chat-first coding workspace with one chronological timeline.
- Coding sessions expose one public thread identifier: `thread_id`.
- Legacy coding-session schemas are reset instead of migrated forward.
- Embedded web assets are rebuilt from the current chat-only frontend.
