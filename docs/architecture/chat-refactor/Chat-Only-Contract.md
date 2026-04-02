---
type: analysis
title: Chat-Only Contract
created: 2026-04-01
tags:
  - chat
  - refactor
  - legacy multi-runner-removal
related:
  - '[[Chat-Legacy-Cleanup-Matrix]]'
---

# Chat-Only Contract

## Scope

Phase 01 converts `/chat` into a single-lane coding chat experience. The public contract must be defined by the existing normal chat flow, not by latent legacy multi-runner or supervisor/executor machinery still present in backend runtime code or persisted session state.

This contract is based on the current `/chat` path across:

- `web/src/views/CodingView.svelte`
- `web/src/views/coding/*`
- `web/src/lib/coding/*`
- `internal/httpapi/server_admin.go`
- `internal/httpapi/server_coding_ws.go`
- `internal/httpapi/server_coding_compact.go`
- `internal/service/coding_sessions.go`
- `internal/service/coding_modes.go`
- `internal/service/coding_chat_hydration.go`
- `internal/service/coding_legacy_runtime*.go`
- `internal/store/store_coding.go`
- `internal/store/store_schema.go`

## Current Public Session Flow

1. `POST /api/coding/sessions` calls `handleWebCodingSessions`, which delegates to `Service.CreateCodingSession`.
2. `CreateCodingSession` normalizes model, reasoning, workdir, and sandbox values, ensures the managed `.codex` project agents file exists, starts a persisted Codex app-server thread in `CommandMode: "chat"`, and stores the session with the returned chat thread id.
3. `CodingView.svelte` bootstraps by loading sessions, selecting one session, and opening a modal when no session exists yet.
4. `sendMessage()` always sends a websocket payload with `command: "chat"` and `session_intent: "chat"`.
5. The message surface already behaves as a single visible timeline in the main path because `predictedRunnerRole()` returns `chat` and all dual-lane UI guards currently return `false`.

## What Must Stay In `/chat`

The public experience must continue to expose these controls and message types during Phase 01:

- Workspace picker and workdir persistence for new and existing sessions.
- Model selector and reasoning selector.
- Sandbox mode toggle.
- Slash command parsing in the composer path.
- Skill picker and skill token insertion.
- Stop and force-stop behavior through existing websocket control plumbing.
- Session drawer, session selection, and session deletion.
- One chronological timeline that can show:
  - assistant replies
  - terminal / exec activity
  - MCP activity
  - subagent lifecycle and summaries
  - file activity for `Edited`, `Created`, `Deleted`, `Moved`, `Renamed`, and `Read`
- Existing compact message rendering and deduplication helpers rather than a replacement UI system.

## What Phase 01 Must Stop Exposing Publicly

These concepts are legacy and must not shape normal `/chat` behavior anymore:

- Legacy multi-runner mode as a visible user-facing mode.
- Dual-lane UI framing, including supervisor lane and working/executor lane presentation.
- Lane-specific request or response expectations in the normal chat path.
- Legacy multi-runner session flags as meaningful public state.
- Legacy multi-runner hydration handoff as part of normal fresh-session bootstrap.
- Legacy multi-runner-specific controls, wording, or metadata in the visible prototype path.

## Contract Boundary

### Stable Phase 01 public contract

These are stable and should remain first-class:

- `coding_sessions` fields for `title`, `model`, `reasoning_level`, `work_dir`, `sandbox_mode`, `codex_thread_id`, `chat_codex_thread_id`, timestamps, and normal message storage.
- Template/bootstrap behavior that prepares the workspace and `.codex` runtime home before chat begins.
- Existing message parsing helpers in `activityParsing.js`, `liveMessagePipeline.js`, `messageMerge.js`, and `messageView.js`.
- Current top bar, composer, session drawer, skill modal, status line, and message panes.

### Legacy Phase 01 removal boundary

These still exist in code or storage, but should be treated as transition-only internals during the refactor:

- `legacy_enabled`
- `legacy_supervisor_thread_id`
- `legacy_executor_thread_id`
- `chat_needs_hydration`
- `chat_context_version`
- `last_hydrated_chat_context_version`
- `last_mode_transition_summary`
- `legacy_plan_artifact_path`
- `legacy_plan_updated_at`
- websocket `lane_projections`
- lane-scoped compact view storage such as `lane_chat`, `lane_executor`, and `lane_supervisor`
- legacy multi-runner resume, recovery, artifact discovery, and handoff code in `coding_legacy_runtime.go` and `coding_chat_hydration.go`
- public mode switching that still recognizes `legacy multi-runner`

## Phase 01 Refactor Rules

- Fresh sessions must start as normal chat sessions with a single persisted chat thread.
- `/chat` must not depend on supervisor or executor ownership to render normal activity rows.
- Legacy actor values such as `supervisor` and `executor` may still appear in imported or historical data, but they should be normalized as compatibility input, not as first-class visible lanes.
- Existing activity bubble types stay intact; the change is to projection and public framing, not to remove terminal, MCP, subagent, or file-op detail.
- If a code path still requires legacy multi-runner artifacts or lane projections for normal `/chat`, that path is outside the Phase 01 contract and should be treated as refactor debt.

## Evidence Notes

- `CodingView.svelte` already forces single-lane display by returning `false` from `hasAutopilotDualLanes()`, `shouldShowOrchestratorLane()`, and `shouldShowExecutorLane()`.
- `sendMessage()` already sends `command: "chat"` and `session_intent: "chat"`.
- `Service.CreateCodingSession()` already starts a chat thread directly and persists it into `CodexThreadID` plus `ChatCodexThreadID`.
- `server_coding_ws.go` still emits `lane_projections`, and the store schema still persists legacy multi-runner and hydration fields, which is why a cleanup matrix is required in [[Chat-Legacy-Cleanup-Matrix]].
