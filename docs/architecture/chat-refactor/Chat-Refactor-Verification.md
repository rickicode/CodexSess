---
type: report
title: Chat Refactor Verification
created: 2026-04-01
tags:
  - chat
  - cleanup
  - verification
related:
  - '[[Chat-Only-Contract]]'
  - '[[Chat-Legacy-Cleanup-Matrix]]'
  - '[[Chat-Bubble-Parity]]'
---

# Chat Refactor Verification

## Sweep Scope

This report starts the Phase 04 removal ledger for the `/chat` refactor. The 2026-04-02 sweep searched the repository for legacy `/chat` terms before any additional purge work:

- `rg -n "legacy multi-runner|supervisor|executor|hydration|dual-lane|mode switcher|hydrated from latest legacy multi-runner artifacts|supervisor lane|executor lane" README.md README.id.md CHANGELOG.md docs/architecture/chat-refactor docs/implementation-log.md`
- `rg -l "legacy multi-runner|supervisor|executor|hydration|dual-lane|mode switcher|hydrated from latest legacy multi-runner artifacts|supervisor lane|executor lane" internal web --glob '!internal/webui/assets/**' | sort`
- `git ls-files | rg "coding_legacy_runtime|coding_chat_hydration|coding_modes|legacy multi-runner"`

## Deleted Or Already Gone

The sweep confirmed that several Phase 02/03 targets are no longer tracked in the repository:

- `internal/service/coding_legacy_runtime.go`
- `internal/service/coding_chat_hydration.go`
- `internal/service/coding_modes.go`

These deletions are preserved here because the matrix and implementation log still reference them as completed cleanup work.

## Intentionally Preserved References

Some search hits are expected survivors and should not be purged:

| Surface | Why it remains | Disposition |
| --- | --- | --- |
| `docs/architecture/chat-refactor/Chat-Only-Contract.md`, `docs/architecture/chat-refactor/Chat-Legacy-Cleanup-Matrix.md`, `docs/architecture/chat-refactor/Chat-Bubble-Parity.md` | Historical/spec documents for the refactor itself | `keep-for-non-chat meaning` |
| `docs/implementation-log.md`, `CHANGELOG.md` | Audit trail of earlier migrations and releases | `keep-for-non-chat meaning` |
| `internal/service/defaults/codex-agents/it-ops-supervisor.toml`, `internal/service/defaults/codex-agents/workflow-supervisor.toml` | Codex subagent template names unrelated to `/chat` mode semantics | `keep-for-subagent-runtime` |
| `internal/service/defaults/codex-agents/nextjs-developer.toml`, `internal/service/defaults/codex-agents/vue-expert.toml` | Generic frontend "hydration" terminology in subagent guidance, not legacy multi-runner hydration handoff behavior | `keep-for-subagent-runtime` |

## Residual Legacy Risk

The sweep still found live code and tests that teach the old legacy multi-runner/supervisor/executor model. These are the remaining purge targets for later Phase 04 tasks:

| Bucket | Representative files | Planned action |
| --- | --- | --- |
| Store compatibility fields and migration-only assertions | `internal/store/coding_session.go`, `internal/store/store_schema.go`, `internal/store/store_schema_migration_test.go`, `internal/store/coding_sessions_legacy_test.go` | Delete the temporary legacy multi-runner fields and replace test names/assertions with chat-only wording |
| Backend runtime role/error branches | `internal/service/coding_appserver_runtime.go`, `internal/service/coding_runtime.go`, `internal/service/coding_stream.go`, `internal/httpapi/server_coding_compact.go`, `internal/httpapi/server_coding_errors.go`, `internal/httpapi/server_coding_ws.go` | Delete hidden dual-runner branches and remove legacy multi-runner-coded runtime/error terminology |
| Frontend compatibility parsing and labels | `web/src/lib/coding/activityParsing.js`, `web/src/views/CodingView.svelte`, `web/src/views/coding/messageView.js` | Simplify to chat-only semantics and rename any surviving compatibility helpers |
| Backend and frontend test names/fixtures | `internal/httpapi/server_coding*_test.go`, `internal/service/coding_test.go`, `web/src/views/coding/*.test.js`, `web/src/lib/coding/messageMerge.test.js` | Rewrite or remove tests whose names still teach the old model |
| Provider defaults that leak legacy actor names | `internal/provider/codex_appserver_runner.go` | Rename the public-facing fallback actor away from `executor` if the behavior still needs a label |

## Product-Facing Description Updates

The dedicated product-copy pass is now complete:

- [`README.md`](../../../README.md) now describes `/chat` as the normal browser coding workspace, with one persistent conversation timeline and Codex-style event bubbles instead of mode-switch wording.
- [`README.id.md`](../../../README.id.md) mirrors the same product framing in Indonesian and now includes a dedicated `/chat` workspace section.
- [`CHANGELOG.md`](../../../CHANGELOG.md) includes an `Unreleased` entry that describes the public `/chat` refactor in release-note terms.

## Notes For The Remaining Phase 04 Work

- `README.md`, `README.id.md`, and `CHANGELOG.md` now reflect the chat-only `/chat` contract; the remaining work is implementation verification plus any final legacy-reference cleanup discovered during the last grep/smoke pass.
- This report is intentionally incremental; the later verification task should append exact test/build/smoke commands plus any justified leftovers that survive the final purge.

## Final Verification Commands

The Phase 04 verification pass completed on 2026-04-02 with explicit timeout-bounded commands:

- `rtk timeout 120s go test ./...` -> pass when run sequentially after the frontend asset build finished
- `cd web && rtk timeout 120s npm run test:unit` -> pass
- `cd web && rtk timeout 120s npm run build:web` -> pass
- `cd web && rtk timeout 120s node --test src/views/coding/messageView.test.js` -> pass after removing the obsolete non-chat pending-id compatibility branch
- `rtk grep -n "legacy multi-runner|legacy_|codingModeAutopilot|Chat hydrated from latest legacy multi-runner artifacts|supervisor lane|executor lane" .` -> only migration, negative-assertion, and audit-trail survivors remain

## Smoke Coverage

- `rtk timeout 120s go test . -run TestAppStartup_MigratesLegacyCodingSessionsAndPreservesChatLifecycle` starts the app against a temporary `HOME`, seeds a legacy `coding_sessions` schema, logs in through `/api/auth/login`, exercises `/api/coding/sessions`, websocket chat send/stop flow, and verifies the migrated `/chat` session stays on the one-thread chat contract. This passed both directly and inside the green sequential `rtk timeout 120s go test ./...` run.
- The retained `/chat` controls and bubble categories remain covered by the existing Phase 03 frontend contracts that also stayed green during this pass:
  - `cd web && rtk timeout 120s npm run test:unit`
  - `web/src/views/coding/CodingView.contract.test.js` still locks the retained controls and single-pane chat shell copy
  - `web/src/views/coding/messageView.test.js` still locks assistant, exec, MCP, subagent, stderr, and file-operation bubble behavior on the single timeline

## Justified Leftovers

The final grep still finds a small set of deliberate survivors:

| Surface | Why it remains | Disposition |
| --- | --- | --- |
| `internal/store/store_schema.go`, `internal/store/store_schema_migration_test.go`, `main_smoke_test.go` | Legacy column names remain only to detect and migrate pre-refactor `coding_sessions` tables correctly | `keep-for-migration compatibility` |
| `internal/httpapi/server_test.go`, `internal/httpapi/server_coding_ws_test.go`, `internal/service/coding_test.go` | Negative assertions that legacy legacy multi-runner-shaped payloads, settings, resume logs, and lane-only output stay absent | `keep-for-regression coverage` |
| `web/src/views/coding/CodingView.contract.test.js` | Negative assertions that removed legacy multi-runner selectors and copy do not reappear in the frontend | `keep-for-regression coverage` |
| `internal/provider/codex_appserver_runner.go`, `internal/service/coding_appserver_runtime.go`, `internal/service/coding_runtime.go`, `internal/httpapi/server_coding_compact.go`, `internal/httpapi/server_coding_ws.go` | Internal runtime compatibility still normalizes historical `executor`/`supervisor` actor labels and recovery rows for stored events and Codex runtime plumbing; these are no longer exposed as `/chat` mode-switch semantics | `keep-for-runtime compatibility` |
| `docs/architecture/chat-refactor/*.md`, `docs/implementation-log.md`, `CHANGELOG.md` | Historical design and release record for the refactor itself | `keep-for-non-chat meaning` |

## Residual Risk

- The only non-doc legacy hit removed during this verification task was the obsolete `pending-*` live-id compatibility branch in `web/src/views/coding/messageView.js`; no other remaining grep hit requires production cleanup to keep `/chat` truthful today.
- Sequential verification matters for the root Go suite because `internal/webui` embeds hashed frontend assets. Running `go test ./...` in parallel with `npm run build:web` can create a transient embed mismatch while the built asset names are being rewritten, so the final verification result is the sequential green run recorded above.

## Final Diff Audit

- This closeout pass removed two more naming residues from the chat-refactor scope: `web/src/views/CodingView.svelte` no longer reads `last_mode_transition_summary`, and the managed AGENTS helper moved from `internal/service/coding_legacy_agents*.go` to `internal/service/coding_project_agents*.go`.
- Fresh verification for those edits passed:
  - `cd web && rtk timeout 120s node --test src/views/coding/CodingView.contract.test.js`
  - `rtk timeout 120s go test ./internal/service -run 'TestEnsureCodingProjectAgentsFile|TestCreateCodingSession'`
  - `rtk grep -n "last_mode_transition_summary|coding_legacy_agents|legacy_agents" web internal docs .maestro`
- Follow-up audit evidence confirmed the removed legacy filenames and helper names are now doc-only survivors:
  - `rtk grep -n "coding_legacy_runtime\.go|coding_chat_hydration\.go|coding_modes\.go|coding_legacy_agents|legacy_agents" .`
  - `rtk grep -n "last_mode_transition_summary" web internal .`
- Fresh closeout verification on 2026-04-02 also passed:
  - `rtk timeout 120s go test ./...`
  - `rtk timeout 120s go test . -run 'TestAppStartup_MigratesLegacyCodingSessionsAndPreservesChatLifecycle'`
  - `cd web && rtk timeout 120s npm run test:unit`
  - `cd web && rtk timeout 120s npm run build:web`
  - `git ls-files | rg 'coding_legacy_runtime|coding_chat_hydration|coding_modes|legacy_agents'`
  - `rtk git diff --name-only -- . ':(exclude)AGENTS.md' ':(exclude)internal/webui/webui.go' ':(exclude)internal/webui/webui_test.go'`
- `rtk git diff --name-only` and `rtk git status --short` still show unrelated pre-existing worktree edits in `AGENTS.md` and `internal/webui/*`, but the exclude-scoped diff is empty, so there is no remaining uncommitted chat-refactor work. The Phase 04 playbook closeout is complete; the leftover worktree dirt belongs to a separate, out-of-scope change set.
