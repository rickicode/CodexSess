---
type: analysis
title: Chat Legacy Cleanup Matrix
created: 2026-04-01
tags:
  - chat
  - backend
  - migration
  - cleanup
related:
  - '[[Chat-Only-Contract]]'
---

# Chat Legacy Cleanup Matrix

This matrix expands [[Chat-Only-Contract]] into a file-level backend removal plan for Phase 02. The goal is to remove legacy multi-runner persistence, hydration, and dual-runner bookkeeping from the backend contract without disturbing the surviving chat runtime features that the prototype still needs.

| Surface | Current evidence | Phase 01 status | Required action |
| --- | --- | --- | --- |
| Session creation API | `handleWebCodingSessions` only accepts title/model/reasoning/workdir/sandbox and calls `CreateCodingSession(..., false)` | Keep | Preserve shape and defaults for normal chat bootstrap |
| Chat thread bootstrap | `Service.CreateCodingSession` starts `CommandMode: "chat"` and stores `CodexThreadID` / `ChatCodexThreadID` | Keep | Keep as the only fresh-session bootstrap path |
| Workspace + sandbox + model + reasoning controls | `CodingTopbar`, `CodingComposer`, session preferences update path | Keep | Preserve control surface unchanged |
| Slash command flow | `sendMessage()` parses supported slash commands before websocket send | Keep | Preserve composer slash command path |
| Skill picker | `CodingSkillModal` and skill insertion in composer | Keep | Preserve as-is |
| Stop / force-stop plumbing | `CodingComposer` cancel flow plus websocket control commands | Keep | Preserve existing stop UX |
| Session drawer | `CodingSessionDrawer` plus session CRUD in `CodingView.svelte` | Keep | Preserve as-is |
| Single visible timeline | `CodingView.svelte` main path renders one `CodingMessagesPane` | Keep | Make this the only visible `/chat` layout |
| Exec bubbles | `messageView.js`, `liveMessagePipeline.js`, compact builder helpers | Keep | Preserve rendering and chronological merge |
| MCP bubbles | `activityParsing.js` and live pipeline parse MCP tool activity | Keep | Preserve rendering and chronology |
| Subagent bubbles | `liveMessagePipeline.js` and `messageView.js` normalize lifecycle rows | Keep | Preserve rendering and detail modal |
| File-op bubbles | `activityParsing.js` and compact builder normalize read/create/edit/delete/move/rename activity | Keep | Preserve rendering and dedupe |
| Dual-lane UI sections | `CodingView.svelte` still contains supervisor and working-lane markup, but guards always return `false` | Remove from public path | Delete or fully neutralize lane-only UI branches |
| Public legacy multi-runner mode | `coding_modes.go` still exposes `chat` and `legacy multi-runner` as public modes | Remove from public path | Stop exposing legacy multi-runner as a normal `/chat` mode |
| Legacy multi-runner session flags | `coding_sessions` schema still stores `legacy_enabled` and thread/handoff fields | Transition-only | Stop relying on these for normal chat; remove or quarantine later |
| Legacy multi-runner hydration handoff | `coding_chat_hydration.go` rebuilds chat from legacy multi-runner artifacts | Remove from normal bootstrap | Fresh Phase 01 sessions must never require hydration artifacts |
| Supervisor/executor thread bookkeeping | `legacy_supervisor_thread_id`, `legacy_executor_thread_id`, artifact selection, executor continuation | Remove from visible path | Keep only as migration debt until backend simplification lands |
| Lane projections in websocket snapshots | `server_coding_ws.go` still emits `lane_projections` and loads lane-specific view messages | Remove from normal contract | Replace with a single-timeline snapshot contract |
| Lane-specific compact storage | `coding_view_messages` currently stores `lane_chat`, `lane_executor`, `lane_supervisor` projections | Remove from normal contract | Collapse to canonical/compact chat timeline only |
| Supervisor/executor actor assumptions in frontend projection | `messageMerge.js` and `messageView.js` still normalize these legacy actor values | Transition-only | Retain only as import compatibility while removing first-class lane semantics |
| Legacy multi-runner-specific runtime recovery text | `coding_legacy_runtime.go` and compact builder parse thread resume / recovery / rebootstrap messages | Remove from public path | Do not surface legacy multi-runner wording in the Phase 01 chat prototype |

## Phase 04 Sweep Disposition

Repo-wide legacy sweep rerun on 2026-04-02 across `internal/`, `web/`, `docs/architecture/chat-refactor/`, `docs/implementation-log.md`, `README.md`, `README.id.md`, and `CHANGELOG.md`.

| Remaining reference bucket | Current evidence | Phase 04 disposition | Reasoning |
| --- | --- | --- | --- |
| Historical refactor docs | `docs/architecture/chat-refactor/Chat-Only-Contract.md`, `docs/architecture/chat-refactor/Chat-Legacy-Cleanup-Matrix.md`, `docs/architecture/chat-refactor/Chat-Bubble-Parity.md` still name `legacy multi-runner`, `supervisor`, `executor`, and hydration debt explicitly | `keep-for-non-chat meaning` | These files are the historical design record for the purge itself, not live `/chat` behavior or product copy |
| Historical implementation and release logs | `docs/implementation-log.md` and `CHANGELOG.md` retain older legacy multi-runner-era entries | `keep-for-non-chat meaning` | They document prior releases and migration steps; deleting the words would destroy auditability |
| Already-removed backend legacy files | `git ls-files` no longer returns `internal/service/coding_legacy_runtime.go`, `internal/service/coding_chat_hydration.go`, or `internal/service/coding_modes.go` | `delete` | The core Phase 02/03 backend legacy multi-runner surfaces are already gone from the tracked tree and should stay deleted |
| Store compatibility shims and migration checks | `internal/store/coding_session.go`, `internal/store/store_schema.go`, `internal/store/store_schema_migration_test.go`, `internal/store/coding_sessions_legacy_test.go` still mention legacy multi-runner fields and hydration cleanup | `delete` | These are temporary compatibility/migration surfaces and the remaining legacy multi-runner terminology should disappear once the last schema/test cleanup lands |
| Backend runtime and HTTP legacy role branches | `internal/service/coding_appserver_runtime.go`, `internal/service/coding_runtime.go`, `internal/service/coding_stream.go`, `internal/httpapi/server_coding_compact.go`, `internal/httpapi/server_coding_errors.go`, `internal/httpapi/server_coding_ws.go` still special-case `supervisor`/`executor` or legacy multi-runner-coded errors | `delete` | These branches still teach the old dual-runner mental model in live code and should be removed rather than hidden |
| Frontend compatibility parsing that still uses legacy names | `web/src/lib/coding/activityParsing.js`, `web/src/views/CodingView.svelte`, `web/src/views/coding/messageView.js` still parse or label `supervisor`/`executor` ownership and pending legacy multi-runner markers | `rename` | Some historical-data normalization may survive briefly, but the code should stop naming the old runtime roles as first-class `/chat` concepts |
| Backend and frontend tests with legacy multi-runner-era names | `internal/httpapi/server_coding*_test.go`, `internal/service/coding_test.go`, `internal/store/coding_sessions_legacy_test.go`, `web/src/views/coding/*.test.js`, `web/src/lib/coding/messageMerge.test.js` still assert legacy actor/lane wording | `rename` | Coverage should stay, but the suite must describe chat-only behavior truthfully once the implementation branches are cleaned |
| Provider defaults that mention generic runtime roles | `internal/provider/codex_appserver_runner.go` still defaults stderr events to actor `executor` | `rename` | The transport can preserve behavior without preserving the legacy label in the public `/chat` mental model |
| `.codex` runtime template agent names and generic hydration wording | `internal/service/defaults/codex-agents/it-ops-supervisor.toml`, `internal/service/defaults/codex-agents/workflow-supervisor.toml`, `internal/service/defaults/codex-agents/nextjs-developer.toml`, `internal/service/defaults/codex-agents/vue-expert.toml` | `keep-for-subagent-runtime` | These are Codex subagent/runtime template concepts or generic frontend terms, not `/chat` product behavior |

## Allowed Survivors

These capabilities stay first-class while the backend cleanup lands:

- Chat runtime bootstrapping with one persisted chat thread from `internal/service/coding_sessions.go`.
- `.codex` runtime home setup plus template sync for agents, skills, and MCP from `internal/service/coding_appserver_runtime.go`, `internal/service/coding_template_agents.go`, and `internal/service/coding_template_skills.go`.
- Local command support from `internal/service/coding_local_commands.go`.
- Skill discovery in `internal/httpapi/server_coding_ws.go`.
- Stop and restart controls from `internal/service/coding_runtime.go`, `internal/service/coding_stream.go`, `internal/httpapi/server_coding.go`, and `internal/httpapi/server_coding_ws.go`.
- Live event streaming and compact-history building in `internal/httpapi/server_coding_ws.go` and `internal/httpapi/server_coding_compact.go`.
- Session metadata that still matters to normal `/chat`: `title`, `model`, `reasoning_level`, `work_dir`, `sandbox_mode`, the active chat thread, restart state, timestamps, `artifact_version`, and `last_applied_event_seq`.

## Required Deletions

These legacy backend concepts must be deleted outright, not hidden behind compatibility shims:

- `legacy_enabled`
- `legacy_supervisor_thread_id`
- `legacy_executor_thread_id`
- `legacy_plan_artifact_path`
- `legacy_plan_updated_at`
- `chat_needs_hydration`
- `chat_context_version`
- `last_hydrated_chat_context_version`
- `last_mode_transition_summary`
- Legacy multi-runner mode switching and any `chat -> legacy multi-runner` or `legacy multi-runner -> chat` transition helpers
- Legacy multi-runner hydration and handoff recovery helpers
- Supervisor / executor sibling coordination and actor-specific runtime ownership
- Legacy multi-runner-only error mapping, response fields, replay metadata, and status/reporting branches

## File-Level Removal Map

### `internal/store`

| File | Keep | Delete / rewrite | Affected tests |
| --- | --- | --- | --- |
| `internal/store/types.go` | Core chat session fields, restart state, timestamps, `artifact_version`, `last_applied_event_seq` | `AutopilotEnabled`, `AutopilotOrchestratorThreadID`, `AutopilotExecutorThreadID`, `ChatNeedsHydration`, `ChatContextVersion`, `LastHydratedChatContextVer`, `LastModeTransitionSummary`, `AutopilotPlanArtifactPath`, `AutopilotPlanUpdatedAt` | `internal/store/coding_sessions_legacy_test.go` must be replaced with chat-only persistence assertions |
| `internal/store/store_coding.go` | Session CRUD for one chat thread plus event-sequence persistence | `normalizeSharedExecutorChatThread`, `applySharedExecutorChatThread`, legacy multi-runner column scan/update logic, legacy multi-runner normalization branches in `Create/List/Get/UpdateCodingSession` | `internal/store/coding_sessions_legacy_test.go`, `internal/service/coding_test.go`, `internal/httpapi/server_coding_ws_test.go` |
| `internal/store/store_schema.go` | `coding_sessions` table, `restart_pending`, `artifact_version`, `last_applied_event_seq` | Legacy column additions for `legacy_*`, hydration fields, and in-place resets that keep dead columns alive; replace with explicit table rebuild and fresh chat-only rows | `internal/store/store_schema_migration_test.go` must prove the rebuilt schema drops the old columns and wipes stale legacy state |

### `internal/service`

| File | Keep | Delete / rewrite | Affected tests |
| --- | --- | --- | --- |
| `internal/service/coding_sessions.go` | Chat-only `CreateCodingSession`, session CRUD, one-thread bootstrap | Legacy boolean legacy multi-runner argument and any compatibility branching around it | `internal/service/coding_test.go`, `internal/httpapi/server_test.go` |
| `internal/service/coding_runtime.go` | `RestartCodingRuntime`, `CodingRuntimeStatusDetail`, `restart_pending` handling | Runner role normalization that treats `supervisor` and `executor` as normal runtime states | `internal/service/coding_runtime_test.go`, `internal/httpapi/server_coding_ws_test.go` |
| `internal/service/coding_stream.go` | Chat send flow, live event fanout, stop behavior, active run bookkeeping | `legacy multi-runner direct chat runtime is disabled` path, hydration gating, `initialCodingRunActor`, thread rollover helpers that preserve executor/supervisor ownership, legacy multi-runner-specific summaries and continuation branches | `internal/service/coding_test.go`, `internal/httpapi/server_coding_ws_test.go` |
| `internal/service/coding_appserver_runtime.go` | Runtime home creation, `.codex` sync, auth sync, stop/restart scaffolding, one chat runtime home | Runtime snapshot fields for `supervisor_thread_id`, `legacy_supervisor_thread_id`, `legacy_executor_thread_id`, sibling selection/exclusion, role-specific skill syncing that only exists for legacy multi-runner separation | `internal/service/coding_test.go` |
| `internal/service/coding_local_commands.go` | Local command support in chat mode | Messages or guards that tell users to switch to legacy multi-runner | `internal/service/coding_test.go` |
| `internal/service/coding_template_agents.go` | Entire file; survivor | Nothing in this phase | Covered indirectly by runtime-home tests |
| `internal/service/coding_template_skills.go` | Entire file; survivor | Nothing in this phase | Covered indirectly by runtime-home tests |
| `internal/service/coding_modes.go` | Nothing beyond possible constants reused elsewhere | `codingModeAutopilot`, mode switching, legacy multi-runner transition summaries | `internal/service/coding_test.go` |
| `internal/service/coding_chat_hydration.go` | Nothing; delete file after callers are removed | Entire hydration flow, legacy multi-runner artifact reads, revert-to-legacy multi-runner recovery | `internal/service/coding_test.go` |
| `internal/service/coding_legacy_runtime.go` | Nothing; delete file after callers are removed | Planning artifacts, supervisor/executor turns, legacy multi-runner thread resume/rebootstrap, executor handoff, legacy multi-runner-only runtime errors | `internal/service/coding_legacy_test.go`, large portions of `internal/service/coding_test.go` |

### `internal/httpapi`

| File | Keep | Delete / rewrite | Affected tests |
| --- | --- | --- | --- |
| `internal/httpapi/server_coding.go` | Session create/list/delete, stop/restart endpoints, compact message endpoints where still chat-true | Public fields that expose lane or supervisor/executor thread state; compatibility handling that still accepts legacy multi-runner-shaped request data | `internal/httpapi/server_coding_test.go`, `internal/httpapi/server_test.go` |
| `internal/httpapi/server_coding_ws.go` | Session send/stop, live stream events, skill discovery, single timeline replay | `lane_projections`, lane-specific request handling, `chat_context_version`, legacy multi-runner/session metadata projection, supervisor/executor thread ids in mapped payloads | `internal/httpapi/server_coding_ws_test.go` |
| `internal/httpapi/server_coding_compact.go` | Compact builder for assistant, exec, MCP, subagent, file-op, stderr, raw-event rows on one timeline | Lane inference that still treats supervisor/executor as first-class output lanes, legacy multi-runner recovery wording that only exists for dual-runner state | `internal/httpapi/server_coding_compact_test.go`, `internal/httpapi/server_coding_ws_test.go` |
| `internal/httpapi/server_coding_errors.go` | Generic runtime error mapping and stop/retry actions that still apply to chat | Legacy multi-runner hydration pending, supervisor/executor empty-response branches, legacy multi-runner plan artifact sync / metadata persistence errors | `internal/httpapi/server_coding_errors_test.go` |

### Affected Test Rewrite Order

1. `internal/store/store_schema_migration_test.go`: prove a rebuilt `coding_sessions` schema drops legacy multi-runner columns and resets legacy rows into fresh chat-only state.
2. `internal/store/coding_sessions_legacy_test.go`: replace legacy multi-runner persistence expectations with chat-only session persistence coverage or remove the file if it becomes obsolete.
3. `internal/service/coding_test.go` and `internal/service/coding_runtime_test.go`: keep session create/list/send/stop/restart coverage, delete hydration/mode-switch/supervisor-executor expectations, and add chat-only migration/reset assertions.
4. `internal/service/coding_legacy_test.go`: expected to disappear with the legacy multi-runner service file unless a narrow artifact-path utility survives elsewhere.
5. `internal/httpapi/server_coding_test.go` and `internal/httpapi/server_coding_ws_test.go`: lock payload truthfulness for create/list/replay/restart/stop without legacy multi-runner or lane metadata.
6. `internal/httpapi/server_coding_compact_test.go` and `internal/httpapi/server_coding_errors_test.go`: remove legacy multi-runner-only error/recovery expectations while keeping chat timeline rendering coverage.

## Cleanup Order

1. Freeze the contract in [[Chat-Only-Contract]] and protect it with regression tests.
2. Expand the backend cleanup target into this file-level removal map so Phase 02 work stays bounded.
3. Add failing backend regression tests for store, service, and HTTP/chat replay surfaces.
4. Rebuild the `coding_sessions` schema and store/session accessors around one chat thread.
5. Delete legacy multi-runner service/runtime ownership and simplify HTTP/websocket payloads to a truthful chat-only contract.
6. Re-run backend migration, replay, stop/restart, and smoke verification until no legacy multi-runner field survives unintentionally.

## Notes

- The frontend is already halfway through the transition because the dual-lane render guards return `false`.
- The backend still carries the bulk of legacy coupling through mode switching, hydration, lane projections, and legacy multi-runner artifact management.
- Phase 01 should treat historical `supervisor` and `executor` actor values as compatibility input only, not as ongoing UI ownership semantics.
