---
type: note
title: Implementation Log
created: 2026-04-02
tags:
  - implementation-log
  - chat
---

# Implementation Log

## 2026-04-03

- Scope: extracted `/chat` frontend run lifecycle logic out of the monolithic view into dedicated state, transport, stop, and send-completion helpers without changing the chat-first UI contract.
- Files or subsystems touched: `web/src/views/CodingView.svelte`, `web/src/views/coding/CodingView.contract.test.js`, `web/src/views/coding/runStateMachine.js`, `web/src/views/coding/runStateMachine.test.js`, `web/src/views/coding/streamTransport.js`, `web/src/views/coding/stopOrchestration.js`, `web/src/views/coding/stopOrchestration.test.js`, `web/src/views/coding/sendCompletion.js`, and `web/src/views/coding/sendCompletion.test.js`.
- Behavior/runtime effect: `/chat` keeps the same send/stream/stop user flow, but the view now delegates run-phase derivation, state patches, websocket transport, stop handling, and successful completion handling to focused helper modules. This reduces state coupling inside `CodingView.svelte` and makes the run lifecycle testable without rendering the whole view.
- Validation status: `rtk timeout 120s npm run test:unit -- src/views/coding/runStateMachine.test.js src/views/coding/stopOrchestration.test.js src/views/coding/sendCompletion.test.js src/views/coding/CodingView.contract.test.js` passed from `web/`.
- Open follow-up items: the health websocket and the remaining `sendMessage()` orchestration still live in `CodingView.svelte`; they can be extracted later if the team wants a fuller reducer-style state machine.

- Scope: carried subagent identity into wait-completion bubbles and re-verified that assistant timeline ordering stays stable after page refresh in the rebuilt one-origin `/chat` app.
- Files or subsystems touched: `internal/httpapi/server_coding_compact.go`, `internal/httpapi/server_coding_compact_test.go`, `web/src/views/coding/liveMessagePipeline.test.js`, and final rebuilt `/chat` verification flows.
- Behavior/runtime effect: compact/canonical subagent rows now infer nicknames from additional prompt forms such as `Nickname Anda: ...` and `You are nicknamed ...`, allowing `wait_agent` completion rows to render as `Finished waiting for <nickname>` instead of the generic `Subagent wait completed`; rebuilt `make run RUN_PORT=3061` verification also confirmed that assistant bubbles did not collapse to the end of the timeline after refreshing the page in the tested `/chat` scenario.
- Validation status: `rtk timeout 120s go test ./internal/httpapi -run 'TestCodingCompactBuilder_(NicknamedPromptCarriesNicknameIntoWaitCompletion|IndonesianSpawnPromptCarriesNicknameIntoWaitCompletion|SpawnPromptCarriesNicknameIntoWaitCompletion|SubagentSpawnCompletionMergesAndWaitFinalizesRunningRow)$'` passed; `rtk timeout 120s node --test web/src/views/coding/liveMessagePipeline.test.js` passed; rebuilt `make run RUN_PORT=3061` real browser test showed session `71319753-b88d-4c3b-9593-5a9a05162d85` rendering `Finished waiting for Planck.` and session `475782f5-f73d-497b-9350-818be4aad4cc` preserving assistant order across refresh.
- Open follow-up items: none for the verified `/chat` scenarios; any remaining issues would need a new reproducible case.

- Scope: normalized assistant source identity across delta/completion events, preserved first-delta timing per assistant item, and deduped adjacent identical assistant commentary bubbles in the canonical `/chat` timeline.
- Files or subsystems touched: `internal/provider/codex_appserver_runner.go`, `internal/provider/codex_appserver_test.go`, `internal/service/coding_stream.go`, `internal/service/coding_test.go`, `web/src/lib/coding/messageMerge.js`, `web/src/views/coding/messageView.js`, and `web/src/views/coding/messageView.test.js`.
- Behavior/runtime effect: assistant delta events now infer a stable source item type when the app-server omits it; assistant persistence tracks the first delta timestamp per source item so final persisted bubbles stay anchored ahead of later terminal rows; and canonical projection collapses adjacent identical assistant bubbles emitted in a tight window. Real browser verification on the rebuilt one-origin binary (`http://127.0.0.1:3061/chat`) showed a fresh session (`34655dc7-c00c-4f97-8433-06cff4d5a514`) rendering the opening assistant bubble once, before terminal rows, with the follow-up assistant update remaining separate.
- Validation status: `rtk timeout 120s go test ./internal/provider -run 'TestMapAppServerEvent_(PreservesAssistantItemIdentity|InfersAssistantItemTypeFromDeltaMethod)$'` passed; `rtk timeout 120s go test ./internal/service -run 'TestResolveStreamAssistantRecords_(PreservesKnownTimestampsOnCountMismatch|PrefersFirstDeltaTimestampForCompletedAssistant|PreservesStreamTimestamps)$'` passed; `rtk timeout 120s npm --prefix web run test:unit` passed; rebuilt binary + Playwright browser run against `http://127.0.0.1:3061/chat` completed with the duplicate opening assistant bubble removed.
- Open follow-up items: dev-mode frontend on `3051` still attempts websocket connections against the Vite origin instead of the backend origin; that transport bug is separate from the fixed `/chat` bubble-ordering issue.

- Scope: added full timeline regression coverage for the `/chat` bubble-order bug so persisted assistant rows are locked around terminal activity in the rendered canonical timeline.
- Files or subsystems touched: `web/src/views/coding/messageView.test.js`.
- Behavior/runtime effect: coverage now asserts the rendered canonical timeline preserves `assistant -> terminal -> assistant` ordering after live-to-persisted merge, preventing future regressions that would collapse both assistant bubbles to the tail of the turn.
- Validation status: `rtk timeout 120s node --test web/src/views/coding/messageView.test.js` passed.
- Open follow-up items: none.

- Scope: tightened `/chat` live assistant stream merge rules so source-identified assistant commentary opens its own bubble instead of being absorbed into a generic pending placeholder.
- Files or subsystems touched: `web/src/views/CodingView.svelte`, `web/src/views/coding/assistantStreamState.js`, and `web/src/views/coding/assistantStreamState.test.js`.
- Behavior/runtime effect: live assistant events that already carry `source_turn_id`/`source_item_id` identity now only update the matching assistant stream row; if no identity-matched row exists, the UI creates a new assistant bubble instead of reusing an empty generic placeholder, reducing pre-persistence bubble merging during active streaming.
- Validation status: `rtk timeout 120s node --test web/src/views/coding/assistantStreamState.test.js` passed; `rtk timeout 120s npm --prefix web run test:unit` passed.
- Open follow-up items: none.

- Scope: stabilized `/chat` assistant bubble ordering at turn completion so streamed commentary no longer bunches at the end of the timeline after terminal/runtime bubbles.
- Files or subsystems touched: `internal/service/coding_stream.go` and `internal/service/coding_test.go`.
- Behavior/runtime effect: when normalized persisted assistant parts do not have a 1:1 count match with streamed assistant parts, CodexSess now reuses the best available streamed timestamps instead of assigning a fresh `time.Now()` to every assistant bubble; this keeps assistant commentary ordered near its original stream time relative to terminal bubbles.
- Validation status: `rtk timeout 120s go test ./internal/service -run 'TestResolveStreamAssistant'` passed; `rtk timeout 120s npm --prefix web run test:unit` passed.
- Open follow-up items: frontend live assistant fallback-by-actor heuristics were not changed in this patch and should only be revisited if a separate repro shows bubble merging before persistence/final reload.

## 2026-04-02

- Scope: stabilized `terminalInteraction` visibility and preserved streamed assistant commentary timing so transient live rows no longer disappear or collapse unnaturally at turn completion.
- Files or subsystems touched: `internal/provider/codex_appserver_runner.go`, `internal/provider/codex_appserver_test.go`, `internal/service/coding_stream.go`, `internal/service/coding_test.go`, `web/src/views/coding/messageView.js`, and `web/src/views/coding/messageView.test.js`.
- Behavior/runtime effect: `item/commandExecution/terminalInteraction` is now summarized as `Terminal interaction` and treated as a durable activity row instead of transient generic noise; streamed assistant commentary parts now keep their original stream timestamps when persisted, reducing the end-of-turn “messages bunch together” effect.
- Validation status: `rtk timeout 120s go test ./internal/provider -run 'Test(SummarizeAppServerEvent_(MCPReadyIsSuppressed|CommandOutputDeltaEmptySuppressed|TerminalInteractionIsHumanized))$' -count=1` passed; `rtk timeout 120s go test ./internal/service -run 'Test(ResolveStreamAssistantRecords_PreservesStreamTimestamps|SendCodingMessageStream_PrioritizesSubagentAndMCPEventsOverCommandOutputSpam)$' -count=1` passed; `rtk timeout 120s node --test web/src/views/coding/messageView.test.js` passed (`35/35`).
- Open follow-up items: none.

- Scope: fixed provider app-server regressions and simplified non-account schema recovery so `coding_sessions` preserves canonical columns while disposable child state is reset.
- Files or subsystems touched: `internal/provider/codex_appserver_runner.go`, `internal/store/store_schema.go`, `internal/store/store_schema_migration_test.go`, and `main_smoke_test.go`.
- Behavior/runtime effect: canceled RPC calls now remove their pending waiter entry immediately; malformed app-server stdout is logged instead of being silently discarded; structured `error.data` from RPC responses is preserved in surfaced error text; cached persistent app-server processes are no longer tied to the caller context that created them; idle completion now stays open while post-assistant item activity continues; and `coding_sessions` rebuild now preserves supported canonical columns while resetting disposable child chat state (`coding_messages`, snapshots, view rows, ws dedup, and memory items) on schema drift.
- Validation status: `rtk timeout 120s go test ./internal/provider -run 'Test(AppServerClient_(CallRemovesPendingOnContextCancellation|ReadLoopLogsMalformedStdoutLine|ReadLoopPreservesStructuredErrorData)|PersistentAppServerRuntimeCache_ClientSurvivesAcquireContextCancellation)$' -count=1` passed; `rtk timeout 120s go test ./internal/store -run 'TestGetCodingSession_(ExtraColumnsPreserveCanonicalSessionFields|ExtraLegacyColumnsPreserveSessionButDropCodingData|KnownLegacyColumnsPreserveSessionButDropChildState)$' -count=1` passed; `rtk timeout 120s go test . -run 'TestAppStartup_ResetsLegacyCodingSessionsAndPreservesFreshChatLifecycle$' -count=1` passed.
- Open follow-up items: none.

- Scope: ignored local Playwright MCP verification artifacts so browser-debug output stays out of the repository while keeping real `/chat` verification available during development.
- Files or subsystems touched: repository `.gitignore` and verification workflow hygiene.
- Behavior/runtime effect: `.playwright-mcp/` is now excluded from git status, so local Playwright MCP snapshots/logs no longer pollute the worktree when running real browser verification against `/chat`.
- Validation status: `rtk git status --short` no longer reports `.playwright-mcp/` as an untracked path after updating `.gitignore`.
- Open follow-up items: none.

- Scope: humanized search-oriented MCP timeline copy and hid non-user-facing truncation notices from the `/chat` timeline.
- Files or subsystems touched: `web/src/views/coding/messageView.js` and `web/src/views/coding/messageView.test.js`.
- Behavior/runtime effect: search-like MCP rows now render as concise user-facing progress such as `Searching the web` or `Searched code`, while `Event log truncated: ...` noise remains available in persistence/debugging but no longer renders as a visible timeline row.
- Validation status: `rtk timeout 120s node --test web/src/views/coding/messageView.test.js` passed (`34/34`); `rtk timeout 120s node --test web/src/views/coding/liveMessagePipeline.test.js web/src/views/coding/messageView.test.js web/src/views/coding/CodingView.contract.test.js` passed (`67/67`).
- Open follow-up items: none.

- Scope: prioritized persistence of important `/chat` runtime events so subagent and MCP lifecycle rows survive noisy command-output turns instead of disappearing behind truncation.
- Files or subsystems touched: `internal/service/coding_stream.go` and targeted streaming persistence regression coverage in `internal/service/coding_test.go`.
- Behavior/runtime effect: low-signal streaming noise such as raw command output deltas no longer consumes the same persistence budget as important runtime events; `spawn_agent` and MCP lifecycle events are retained in raw chat history under heavy output spam, allowing compact projection to continue rendering subagent and MCP bubbles.
- Validation status: `rtk timeout 120s go test ./internal/service -run 'TestSendCodingMessageStream_PrioritizesSubagentAndMCPEventsOverCommandOutputSpam$' -count=1` failed first because the persisted raw history dropped `spawn_agent`/`mcp__github__search_code` under truncation, then passed after persistence filtering was applied; `rtk timeout 120s go test ./internal/service -run 'TestSendCodingMessageStream_(NormalChatUsesAppServerThreadResume|PrioritizesSubagentAndMCPEventsOverCommandOutputSpam)$' -count=1` passed.
- Open follow-up items: none.

- Scope: implemented Task 5 frontend coverage for live `/chat` bubble boundaries and snapshot-replacement contract stability, and threaded source identity through live projection rows so synthesized stream rows retain app-server identity metadata.
- Files or subsystems touched: `web/src/views/coding/liveMessagePipeline.test.js`, `web/src/views/coding/messageView.test.js`, `web/src/views/coding/CodingView.contract.test.js`, `web/src/views/coding/liveMessagePipeline.js`, `web/src/views/CodingView.svelte`.
- Behavior/runtime effect: added regression tests covering distinct live assistant boundaries and snapshot replacement order contracts; projected exec/subagent/MCP/file rows synthesized from stream events now keep `source_event_type`, `source_thread_id`, `source_turn_id`, `source_item_id`, `source_item_type`, and `event_seq`, so identity-aware replacement and projection keep bubble boundaries stable instead of dropping metadata.
- Validation status: `rtk timeout 120s node --test web/src/views/coding/liveMessagePipeline.test.js web/src/views/coding/messageView.test.js web/src/views/coding/CodingView.contract.test.js` failed first (`2` failing tests: source identity missing on synthesized rows), then passed after implementation changes (`64/64` passing).
- Open follow-up items: none.

- Scope: implemented Task 4 frontend source-identity-first reconciliation so assistant live rows and persisted merge replacement prioritize app-server source turn/item identity over actor/content heuristics.
- Files or subsystems touched: `web/src/lib/coding/messageMerge.js`, `web/src/lib/coding/messageMerge.test.js`, and `web/src/views/CodingView.svelte`.
- Behavior/runtime effect: persisted-vs-live reconciliation now matches source-identified assistant rows by `role|source_turn_id|source_item_id|source_item_type` first, and only falls back to legacy content-based matching for live rows that do not carry source identity; live assistant stream upsert now keys updates by websocket source identity (`source_turn_id`/`source_item_id`/`source_item_type`) and stores source metadata plus a stable `stream_identity_key` on assistant rows to prevent blank-actor coalescing across distinct source items.
- Validation status: `rtk timeout 120s node --test web/src/lib/coding/messageMerge.test.js` failed first on the new identity test, then passed after the implementation changes (`10/10` passing).
- Open follow-up items: this task only added targeted `messageMerge` reconciliation tests; broader live projection/contract tests remain covered by later planned tasks.

- Scope: persisted app-server source identity in compact `/chat` rows so assistant deltas stop collapsing across distinct source items and compact snapshots retain source metadata for assistant, exec, subagent, and MCP-derived rows.
- Files or subsystems touched: compact chat message builder logic and targeted compact builder regression coverage in `internal/httpapi`.
- Behavior/runtime effect: new compact rows now carry `source_event_type`, `source_thread_id`, `source_turn_id`, `source_item_id`, `source_item_type`, and `event_seq` when present on stream events; source-key matching now separates rows by source thread as well as turn/item identity; assistant and internal-runner continuation now match exact source identity first, and only fall back to the legacy actor-based continuation path when source identity is absent.
- Validation status: `rtk timeout 120s go test ./internal/httpapi -run 'TestCodingCompactBuilder_(AssistantIdentitySeparatesItems|SnapshotKeepsSourceIdentity)$'` failed first because assistant deltas merged across distinct source items and compact rows dropped source metadata, then passed after the compact builder changes were applied; `rtk timeout 120s go test ./internal/httpapi -run 'TestCodingCompactBuilder_(InternalRunnerIdentitySeparatesItems|InternalRunnerLegacyFallbackSkipsSourceTaggedRows|InternalRunnerLegacyDeltaFinalStaysSingleRow|AssistantIdentitySeparatesThreads|AssistantIdentitySeparatesItems|SnapshotKeepsSourceIdentity|AssistantDeltaFinalStaysSingleRow)$' -count=1` passed; `rtk timeout 120s go test ./internal/httpapi -run '^TestCodingCompactBuilder_' -count=1` passed.
- Open follow-up items: frontend reconciliation and contract coverage still need the later plan tasks to consume the persisted source identity end-to-end.

- Scope: forwarded provider chat-event source identity metadata through `/api/coding/ws` `session.stream` payloads so live chat streaming can preserve app-server event identity.
- Files or subsystems touched: websocket chat stream serialization, websocket regression coverage, and the stream callback handoff in `internal/service/coding_stream.go` required to keep provider metadata intact up to the websocket boundary.
- Behavior/runtime effect: `session.stream` now forwards `source_event_type`, `source_thread_id`, `source_turn_id`, `source_item_id`, `source_item_type`, `event_seq`, and `created_at` when the provider stream event already has them; existing `stream_type`, `text`, `actor`, `lane`, and `raw_payload` behavior stays unchanged, and blank provider identity fields are not synthesized in the websocket layer.
- Validation status: `rtk timeout 120s go test ./internal/httpapi -run 'TestCodingWS_StreamIncludesSourceIdentity$'` failed first because the websocket payload omitted the forwarded source metadata, then passed after the stream handoff and websocket serialization changes were applied.
- Open follow-up items: compact persistence and frontend reconciliation still need the later plan tasks to consume the websocket metadata end-to-end.

- Scope: preserved app-server source identity on provider chat events so downstream chat streaming and replay can distinguish assistant deltas and completed tool items by original thread/turn/item metadata.
- Files or subsystems touched: provider chat event model, app-server event mapping, and targeted provider identity regression tests.
- Behavior/runtime effect: `provider.ChatEvent` now carries source event type, source thread/turn/item ids, source item type, event sequence, and created-at metadata when app-server params expose them; metadata extraction now preserves common top-level ids and nested `thread.id`, `turn.id`, `item.id`, and nested timestamp shapes; emitted `delta`, `assistant_message`, `activity`, `raw_event`, and `stderr` events keep their prior behavior while adding this metadata.
- Validation status: `rtk timeout 120s go test ./internal/provider -run 'TestMapAppServerEvent_(PreservesAssistantItemIdentity|PreservesCompletedToolIdentity)$'` failed first because nested tool identity fields were blank, then passed after the metadata extractor was fixed and the patch scope was cleaned; `rtk timeout 120s go test ./internal/provider -run 'TestCodexAppServerStreamChatWithOptions_Emits(RawEventIdentityMetadata|StderrIdentityMetadata)$'` passed to cover the `raw_event` startTurn callback wiring and `stderr` subscriber wiring.
- Open follow-up items: websocket serialization, compact persistence, and frontend reconciliation still need the later plan tasks to consume these new provider fields end-to-end.

- Scope: removed the injected `mcp_servers.codex_apps` section from coding template and runtime config sanitization so broken `codex_apps` startup attempts stop being carried into CodexSess runtime homes.
- Files or subsystems touched: coding app-server runtime config merge/sanitization helpers and targeted service regression tests for template sync plus runtime config cleanup.
- Behavior/runtime effect: when CodexSess syncs `~/.codex/config.toml` into its template home or sanitizes an existing per-session runtime `config.toml`, it now strips `[mcp_servers.codex_apps]` the same way it already strips legacy `[mcp_servers.memory]`, while preserving other user config and baseline MCP sections.
- Validation status: `rtk timeout 120s go test ./internal/service -run TestEnsureCodingRuntimeHome_SanitizesLegacyRuntimeSkillsAndMemoryConfig` passed; `rtk timeout 120s go test ./internal/service -run TestEnsureCodingTemplateHome_SyncsUserCodexBaseline` passed.
- Open follow-up items: existing runtime homes that already contain `codex_apps` need one more sanitize/refresh path execution to rewrite their local `config.toml`.

- Scope: removed unused runtime session columns and orphan usage snapshot storage so chat session persistence keeps only actively used schema.
- Files or subsystems touched: store schema migration rules, store schema regression tests, and the local `~/.codexsess/data.db` schema cleanup.
- Behavior/runtime effect: startup now drops the unused `usage_snapshots` table, rebuilds `coding_sessions` to the exact chat-only column set while preserving core session/message data, and no code path references `runtime_mode` or `runtime_status` anymore.
- Validation status: `rtk timeout 120s go test ./internal/httpapi ./internal/service ./internal/store ./internal/provider` passed.
- Open follow-up items: restart the running app with the newly built binary so future launches use the updated schema migrator instead of any previously built executable.

- Scope: hardened account autoswitch so failed active-account usage refreshes recover to another usable account and autoswitch logs show email labels instead of raw `codex_*` ids.
- Files or subsystems touched: usage autoswitch selection/scheduler flow, OAuth/usage test seams, and autoswitch regression tests.
- Behavior/runtime effect: when active CLI or API usage refresh fails, CodexSess now marks that account unhealthy for the tick, retries candidate activation until it finds a usable backup account, and emits autoswitch logs/status labels using account emails when available.
- Validation status: `rtk timeout 120s go test ./internal/httpapi ./internal/service ./internal/provider` passed.
- Open follow-up items: none.

- Scope: surfaced runtime retry progress in the `/chat` status line without adding duplicate retry spam to the timeline.
- Files or subsystems touched: chat view status derivation, message view recovery helpers/tests, and chat contract tests.
- Behavior/runtime effect: while a chat run is recovering from usage-limit or auth/account failures, the status line now shows concise retry phases such as switching accounts or restarting runtime instead of only showing `Streaming...`; timeline recovery rows continue using the existing coalesced milestones.
- Validation status: `rtk timeout 120s node --test web/src/views/coding/liveMessagePipeline.test.js web/src/views/coding/messageView.test.js web/src/views/coding/liveState.test.js web/src/views/coding/CodingView.contract.test.js` passed.
- Open follow-up items: none.

- Scope: filtered protocol-only skill refresh noise from the live `/chat` timeline.
- Files or subsystems touched: live message pipeline ignore rules and live message pipeline tests.
- Behavior/runtime effect: raw `skills/changed` app-server events no longer render as visible activity rows in the chat timeline.
- Validation status: `rtk timeout 120s node --test web/src/views/coding/liveMessagePipeline.test.js web/src/views/coding/messageView.test.js web/src/views/coding/liveState.test.js web/src/views/coding/CodingView.contract.test.js` passed.
- Open follow-up items: none.

- Scope: fixed live `/chat` streaming presentation so the pending assistant placeholder no longer renders as an empty message bubble.
- Files or subsystems touched: message projection rules, coding messages pane markup, live message view tests, and chat layout contract tests.
- Behavior/runtime effect: during active streaming, empty pending assistant placeholders are hidden from the message list and the streaming note renders only as a standalone status row outside message bubbles; after completion, the real assistant bubble still renders normally.
- Validation status: `rtk timeout 120s node --test web/src/views/coding/messageView.test.js web/src/views/coding/liveState.test.js web/src/views/coding/CodingView.contract.test.js` passed.
- Open follow-up items: none.

- Scope: moved `/chat` composer controls below the textarea and simplified the control styling.
- Files or subsystems touched: coding composer Svelte markup, chat composer CSS, and composer layout contract tests.
- Behavior/runtime effect: `Skills`, `Write/Full access`, and `Send` now render in a footer below the textarea, with `Send` staying as the right-side primary action on wider layouts and composer buttons rendering without borders.
- Validation status: `rtk timeout 120s node --test web/src/views/coding/CodingView.contract.test.js` passed.
- Open follow-up items: none.

- Scope: removed bundled local `codex-skills` seeding and made coding template/runtime skills resolve only from the installed `superpowers` repository.
- Files or subsystems touched: coding template skill bootstrap, coding runtime skill sync, user `.codex` template sync rules, service regression tests, and embedded default skill assets removal.
- Behavior/runtime effect: template initialization now fails if the `superpowers` repo is unavailable or missing required `SKILL.md` entries, runtime skill provisioning no longer falls back to local template/user skill directories, and user `~/.codex` no longer gets bundled skill files auto-installed.
- Validation status: `rtk timeout 120s go test ./internal/service -run 'TestEnsureCodingTemplateHome_(SyncsUserCodexBaseline|FailsWithoutSuperpowersRepoEvenWhenLocalSkillsExist)|TestEnsureCodingRuntimeHome_ChatIncludesCoreSuperpowers' -count=1` passed; `rtk timeout 120s go test ./internal/service ./internal/httpapi -count=1` passed.
- Open follow-up items: none.

- Scope: normalized the coding workspace to the current chat-only system snapshot.
- Files or subsystems touched: coding session storage and schema reset, HTTP/websocket session contracts, runtime debug payloads, frontend session display state, embedded web assets, regression coverage, and release/docs metadata.
- Behavior/runtime effect: `/chat` now runs as a single chat-first coding workspace, exposes one public `thread_id`, and drops legacy coding-session rows when an outdated schema is encountered.
- Validation status: `rtk timeout 120s go test ./...` passed; `cd web && rtk timeout 120s npm run test:unit && npm run build:web` passed.
- Open follow-up items: none.
