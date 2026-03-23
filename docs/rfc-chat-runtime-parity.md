# RFC: Coding Chat Runtime V2 (Codex CLI UX Parity)

## 1. Objective

Bring `/chat` UX close to Codex CLI while preserving CodexSess reliability.

Primary goals:
- Stable application-level `session_id`
- Rich real-time event stream (delta, activity, stderr, tool, subagent)
- Runtime lifecycle visibility
- Safe auth-change handling without breaking in-flight work

## 2. Scope

This RFC defines:
- Runtime architecture (hybrid)
- Tradeoffs
- HTTP + WebSocket API contract
- Restart policy when `auth.json` changes

Out of scope (for current increment):
- Full MCP transport runtime
- Subagent process recovery across runtime crashes

## 3. Architecture

### 3.1 Runtime Modes

Each coding session has a runtime mode:
- `spawn` (legacy): spawn `codex exec` per request
- `persistent` (target): long-lived runtime per session (future phases)

### 3.2 Session Runtime Fields

Persisted in `coding_sessions`:
- `runtime_mode`: `spawn|persistent`
- `runtime_status`: `stopped|starting|idle|running|restart_scheduled|restarting|error`
- `restart_pending`: boolean

### 3.3 State Machine

- `stopped -> starting -> idle`
- `idle -> running -> idle`
- `running + auth_changed -> restart_scheduled`
- `restart_scheduled + done -> restarting -> idle`
- failures -> `error` and optional fallback to `spawn`

## 4. UX Parity Targets

Required parity behaviors in `/chat`:
- token delta streaming
- tool/activity stream
- stderr/raw event visibility
- stop/abort support
- runtime status visibility
- explicit restart UX
- subagent event surfaces (started/update/completed)

## 5. API Contract

### 5.1 Existing Session Payload Extension

`GET /api/coding/sessions` and related session payloads include:
- `runtime_mode`
- `runtime_status`
- `restart_pending`

### 5.2 Runtime Management Endpoints

1) `GET /api/coding/runtime/status?session_id=...`
- Returns runtime mode/status + in-flight + started_at

2) `PUT /api/coding/sessions/runtime`
- Body: `{ "session_id": "...", "runtime_mode": "spawn|persistent" }`
- Updates mode and normalizes status

3) `POST /api/coding/runtime/restart`
- Body: `{ "session_id": "...", "force": false }`
- If in-flight and not forced: set `restart_pending=true`, `runtime_status=restart_scheduled`
- If idle: perform immediate runtime reset transition and clear `restart_pending`

### 5.3 WebSocket Runtime Events

Additional WS events:
- `runtime_started`
- `runtime_status`
- `runtime_restart_scheduled`
- `runtime_restarting`
- `runtime_restarted`
- `runtime_error`

## 6. Restart Policy

Default policy:
- Never break in-flight turn for automatic restart.
- If runtime/auth change arrives in-flight: defer (`restart_pending=true`).
- Execute restart immediately after current turn finishes.
- Emit explicit WS/HTTP-visible status transitions.

## 7. Tradeoffs

### Spawn-per-request (current)
Pros:
- simple and robust
- easy auth reload behavior
- strong isolation per turn

Cons:
- process startup overhead per request
- less natural long-lived runtime behavior

### Persistent runtime (target)
Pros:
- lower per-turn overhead
- more CLI-like continuity
- better subagent orchestration potential

Cons:
- lifecycle complexity (reconnect/restart/cleanup)
- auth reload needs explicit policy

### Hybrid recommendation
Use hybrid with `spawn` default and `persistent` opt-in/fallback for safer rollout.

## 8. Rollout Plan

Phase 1 (this increment):
- add runtime fields
- add runtime status/restart/mode APIs
- wire lifecycle status updates around current spawn execution

Phase 2:
- runtime manager abstraction and persistent runtime skeleton

Phase 3:
- auth fingerprint watcher + deferred restart automation

Phase 4:
- full persistent MCP runtime + deeper CLI parity (approval/subagent protocol unification)

## 9. Acceptance Criteria (Phase 1)

- Session includes runtime fields in API responses.
- Runtime status transitions during send flow (`starting/running/idle`).
- Restart endpoint supports deferred restart semantics.
- WebSocket emits runtime status events.
- Existing `/chat` behavior remains backward compatible.
