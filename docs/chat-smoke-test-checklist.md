# `/chat` Smoke Test Checklist

Use this checklist after merging changes that affect `/chat` live streaming, message projection, session lifecycle, or runtime orchestration.

## Session Bootstrap

- [ ] Open `/chat`.
- [ ] Open an existing session.
- [ ] Confirm history loads without UI errors.
- [ ] Confirm the active session id in the URL matches the selected session.

## Assistant-Only Run

- [ ] Send a prompt expected to return only assistant text.
- [ ] Confirm the user bubble appears immediately.
- [ ] Confirm the assistant bubble appears during streaming.
- [ ] Confirm the final assistant bubble remains after completion.
- [ ] Refresh the page and confirm the same assistant bubble remains in the timeline.

## Terminal/Exec Run

- [ ] Send a prompt expected to trigger command execution.
- [ ] Confirm an exec/terminal row appears during the run.
- [ ] Confirm terminal status and output summary settle correctly.
- [ ] Refresh the page and confirm assistant/exec ordering is preserved.

## Subagent Run

- [ ] Send a prompt expected to trigger a subagent.
- [ ] Confirm a subagent row appears during streaming.
- [ ] Confirm the final subagent title/status remains sensible after completion.
- [ ] Refresh the page and confirm the subagent row stays consistent.

## Refresh During Active Run

- [ ] Start a run and refresh the page before it completes.
- [ ] Confirm the session reloads without timeline corruption.
- [ ] Confirm the canonical history remains coherent after reload.
- [ ] Confirm no duplicate assistant/exec/subagent bubbles appear.

## Stop / Force Stop

- [ ] Start a run and click `Stop`.
- [ ] Confirm the primary action changes to `Force Stop`.
- [ ] Confirm the status line reflects stopping state.
- [ ] Repeat with `Force Stop`.
- [ ] Confirm no false composer error appears from websocket detach/background handling.

## Background Detach Recovery

- [ ] Start a run.
- [ ] Simulate a temporary disconnect or refresh while the run is active.
- [ ] Confirm the UI does not immediately degrade into a hard failure state.
- [ ] Confirm the result still appears after synchronization resumes.

## Session Switching

- [ ] Switch to a different session and back again.
- [ ] Confirm draft/session state does not leak across sessions.
- [ ] Confirm lifecycle state from the previous session does not remain active.

## Local Commands

- [ ] Run `/status`.
- [ ] Run `/mcp`.
- [ ] Confirm local command handling still works and does not break the chat timeline.

## API / Canonical History

- [ ] Call `GET /api/coding/messages?session_id=<id>&view=compact`.
- [ ] Confirm returned rows match the visible `/chat` timeline.
- [ ] Confirm the response source is `canonical` on the normal path.

## Targeted Verification Commands

Backend:

```bash
rtk timeout 120s go test ./internal/httpapi -run 'TestCompactRowMatchesCanonicalProjectionForSingleEvent|TestBuildCompactRowFromChatEvent_|TestHandleWebCodingWS_SessionStreamIncludesCompactRow|TestCodingWS_StreamIncludesSourceIdentity|Test(PersistCompactCodingView_SanitizesAndPersistsBothStores|RebuildCompactCodingViewFromRawHistory_PersistsCanonicalRows|HandleWebCodingMessages_(CompactFallbackBuildsCanonicalFromRawHistory_Endpoint|RedactsLegacyUnsanitizedExecOutput|RebuildsWhenCanonicalRowsAreStale|RebuildsWhenCanonicalRowsContainRawProtocolNoise|RebuildsFromRawHistoryWhenSnapshotIsEmpty|RePersistsSanitizedSnapshotRows|SnapshotRehydrationStillReturnsCanonicalSource)|HandleWebCodingMessageSnapshot_(SanitizesBeforePersistence|RebuildsWhenClientSnapshotIsIncomplete))' -count=1
```

Frontend:

```bash
cd web
rtk timeout 120s npm run test:unit -- src/views/coding/CodingView.contract.test.js src/views/coding/liveMessagePipeline.test.js src/lib/coding/messageMerge.test.js src/views/coding/runStateMachine.test.js src/views/coding/stopOrchestration.test.js src/views/coding/sendCompletion.test.js
```

## Pass Criteria

- [ ] Live stream and reload stay aligned for assistant/exec/subagent rows.
- [ ] Stop/Force Stop does not produce false failure UI.
- [ ] Session switching does not leak lifecycle state.
- [ ] Compact history remains canonical-first.
