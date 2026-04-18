---
type: note
title: Implementation Log
created: 2026-04-02
tags:
  - implementation-log
  - chat
---

# Implementation Log

## 2026-04-15

- Scope: evolved the browser session status flow from inferred `missing` completion into explicit terminal states so the UI can distinguish pending, success, and failed browser-login outcomes without guessing.
- Files or subsystems touched: `internal/service/oauth.go`, `internal/httpapi/server_admin.go`, `web/src/App.svelte`, and this implementation log.
- Behavior/runtime effect: browser login status now records short-lived terminal outcomes in backend memory and returns explicit `success`, `error`, or `cancelled` states with sanitized result metadata. The browser login UI now treats `success` as the definitive completion signal, uses `error`/`cancelled` to enter recovery mode, and no longer depends on `missing` plus account-list inference as the main source of truth.
- Validation status: in-session diagnostics passed for the touched backend/admin files with only pre-existing/non-blocking frontend hints and non-blocking Go hints remaining; focused tests/build/diff review are still pending.
- Open follow-up items: add a targeted HTTP test for `/api/auth/browser/status` terminal payloads and, if needed later, surface more polished terminal messages in the UI instead of reusing generic status text.

- Scope: added a lightweight backend browser-session status flow so the web UI can observe pending browser login state directly instead of relying only on account-list polling.
- Files or subsystems touched: `internal/service/oauth.go`, `internal/httpapi/server_admin.go`, `internal/httpapi/routes_web.go`, `web/src/App.svelte`, and this implementation log.
- Behavior/runtime effect: codexsess now exposes a read-only browser session status endpoint for the current/passed `login_id`, returning sanitized pending-session state without exposing OAuth secrets. The browser login UI now checks that status first and only refreshes the account list after the pending session disappears, while keeping the localhost:1455 callback model and manual callback fallback unchanged.
- Validation status: in-session diagnostics passed for the new backend/service route changes with only pre-existing/non-blocking frontend hints remaining; targeted tests/build/diff review are still pending.
- Open follow-up items: add explicit success/error terminal states to the status endpoint if richer browser-session UX is needed later, and consider a dedicated browser-login test covering the new status handler shape.

- Scope: added lightweight inline validation for the manual browser-callback recovery input so users get immediate feedback before submitting malformed localhost callback URLs.
- Files or subsystems touched: `web/src/App.svelte` and this implementation log.
- Behavior/runtime effect: the manual callback fallback now validates that the pasted URL is a real `http://localhost:1455/...` callback and contains both `code` and `state` before submission. Recovery stays available, but obvious wrong-host or incomplete callback URLs now fail fast in the UI instead of only after a backend request.
- Validation status: in-session Svelte diagnostics showed only pre-existing/non-blocking hints; frontend build and final diff review are still pending.
- Open follow-up items: if needed later, add session-specific validation/error handling for wrong-state callback URLs and consider a dedicated backend session-status endpoint so the browser flow doesn’t depend only on account-list polling.

- Scope: simplified the web/admin browser-login modal so one primary browser action area and one secondary manual fallback area are visually distinct without changing the existing OAuth flow.
- Files or subsystems touched: `web/src/App.svelte`, `web/src/app.css`, and this implementation log.
- Behavior/runtime effect: the browser-login modal now leads with a single normal primary section for opening the login URL on the same machine, keeps the `localhost:1455` callback expectation explicit with shorter copy, and moves manual callback completion into a plainer secondary fallback section instead of a multi-step progress panel. Existing browser start/cancel/complete behavior and manual callback submission remain unchanged.
- Validation status: code edits applied; final diff review still pending.
- Open follow-up items: if users still miss the localhost requirement, consider a later inline validation/error pass for the fallback input rather than adding more explanatory UI.

- Scope: added an explicit staged progress experience to the web/admin browser-connect modal so the localhost-based OAuth path reads closer to the real Codex browser flow without changing the backend API contract.
- Files or subsystems touched: `web/src/App.svelte`, `web/src/app.css`, and this implementation log.
- Behavior/runtime effect: the browser login modal now shows a clear progress panel with `ready`, `opened`, `waiting`, `recovery`, and success-state handling semantics, updates status/copy to clarify that OpenAI/Codex returns to `localhost:1455` on the same machine, and reframes manual callback paste as a recovery path for the same login session instead of implying codexsess is the primary callback receiver. Existing `/api/auth/browser/start`, `/api/auth/browser/cancel`, and `/api/auth/browser/complete` behavior remains unchanged.
- Validation status: code edits applied; final targeted frontend diagnostics/build verification still pending.
- Open follow-up items: consider adding lightweight inline validation for pasted callback URLs so recovery errors can explain malformed or wrong-session callback links before submission.

- Scope: rolled the browser/web OAuth start flow back to the Codex-required localhost callback model after verifying that forcing a hosted `/auth/callback` redirect causes OpenAI Codex authentication to fail with `unknown_error`.
- Files or subsystems touched: `internal/service/oauth.go`, `internal/service/oauth_browser_web_test.go`, and this implementation log.
- Behavior/runtime effect: `StartBrowserLoginWeb(...)` once again generates OAuth URLs with `redirect_uri=http://localhost:1455/auth/callback` and stores that localhost redirect in pending login state, while preserving the backend-managed pending-session model, active web-session reuse, and cancel behavior. This keeps codexsess compatible with the real Codex OAuth expectation while still allowing web/admin initiation and backend polling/manual-complete fallback.
- Validation status: in-session LSP diagnostics passed for the service code and browser-web tests; focused Go tests for browser web login/cancel and existing `oauthBaseURLFromRequest` HTTP helpers passed. A broader `./internal/service ./internal/httpapi ./internal/store ./internal/config` run still shows an unrelated pre-existing failure in `TestSendCodingMessageStream_PersistsAssistantAtFirstDeltaTime` (`codex app-server closed`).
- Open follow-up items: redesign the web/admin browser-connect UX to better mirror CLIProxyAPI while still using localhost:1455 for the actual OAuth redirect, and decide later whether the manual callback UI should be simplified or retained as an explicit recovery path.

- Scope: fixed the browser-based account connect flow so web/admin OAuth now uses a true codexsess server callback URL instead of silently falling back to the localhost CLI callback listener.
- Files or subsystems touched: `internal/service/oauth.go`, new `internal/service/oauth_browser_web_test.go`, and this implementation log.
- Behavior/runtime effect: `StartBrowserLoginWeb(...)` now builds OAuth auth URLs using `<externalBaseURL>/auth/callback`, saves pending sessions with that server callback redirect URI, and returns a backend-managed browser login session without binding `127.0.0.1:1455` or waiting on a localhost callback channel. Existing `/auth/callback` and `/api/auth/browser/callback` handlers now match the service-layer flow, while manual callback completion remains available as a fallback path.
- Validation status: in-session LSP diagnostics passed for `internal/service/oauth.go`; new focused service tests cover external callback redirect generation, active web-session reuse, and pending-session cancellation. Broader Go test/diff review still pending.
- Open follow-up items: optionally remove or de-emphasize the manual callback paste UI in `web/src/App.svelte`, and consider adding an HTTP-level callback completion test once token-exchange mocking is convenient in the server layer.

- Scope: finished the current `translator_tools.go` cleanup pass by deleting the obsolete package-level helper bodies after runtime and tests had already been moved onto translator-owned implementations.
- Files or subsystems touched: `internal/httpapi/translator_tools.go` and this implementation log.
- Behavior/runtime effect: `translator_tools.go` no longer contains the old package-level implementations for tool-call parsing, provider mapping, filtering, schema lookup, required-field extraction, prompt tool serialization, or tool-argument normalization. Those behaviors now live behind translator-owned internals in `openAITranslator`, while runtime behavior and existing translator entrypoints remain unchanged.
- Validation status: in-session LSP diagnostics passed for `translator_tools.go`, `translator_openai.go`, `translator_prompt.go`, and the related tool-call test file; broader tests/diff review still pending.
- Open follow-up items: continue reducing package-level helper leakage in the remaining translator files, and optionally remove or consolidate any now-empty/low-value helper files if the slimmer structure is stable after more cleanup passes.

- Scope: continued the deeper `translator_tools` cleanup by internalizing the remaining parse/filter/provider-call helper bodies into `openAITranslator` so runtime behavior no longer depends on those package-level implementations directly.
- Files or subsystems touched: `internal/httpapi/translator_openai.go` and this implementation log.
- Behavior/runtime effect: `openAITranslator` now owns the implementation of tool-call text parsing, provider-call mapping, filtered native-call normalization, tool-definition lookup, required-field extraction, prompt tool serialization, and tool-argument normalization internally. Existing translator entrypoints and tests continue to work, but the package-level helper bodies in `translator_tools.go` are no longer the active runtime implementation path for those behaviors.
- Validation status: in-session LSP diagnostics passed for the touched translator, prompt, policy, and test files; broader tests/diff review still pending.
- Open follow-up items: finish shrinking `translator_tools.go` by removing or privatizing the now-unused package-level helper bodies once all remaining callers are confirmed gone, and decide whether any still-desired internal-unit tests should be migrated to explicit translator entrypoints before final helper deletion.

- Scope: continued the schema-helper cleanup by routing schema-definition behavior through translator internals and moving prompt tool serialization off direct package-level helper usage.
- Files or subsystems touched: `internal/httpapi/translator_openai.go`, `internal/httpapi/translator_prompt.go`, and this implementation log.
- Behavior/runtime effect: the OpenAI translator now owns the internal seam for tool-definition name lookup, missing-required-field checks, prompt tool serialization, and related schema helper access, instead of exposing those details as direct package-level runtime dependencies. Prompt tool injection now serializes through translator internals while preserving the existing `AVAILABLE_TOOLS_JSON` payload shape and all current OpenAI/Claude behavior.
- Validation status: in-session LSP diagnostics passed for the touched translator, prompt, policy, and test files; broader tests/diff review still pending.
- Open follow-up items: continue shrinking `translator_tools.go` by migrating the remaining parse/filter/provider-call helper bodies behind translator-owned internals, and later decide whether the remaining direct internal helper tests should gain explicit object entrypoints or stay as intentional package-level unit coverage.

- Scope: continued the sanitize/helper cleanup by moving Claude tool-result sanitization behind policy entrypoints, deleting the dead package-level `resolveToolCalls(...)` helper, and shrinking runtime/test dependence on package-level tool/schema helpers.
- Files or subsystems touched: `internal/httpapi/claude_policy.go`, `internal/httpapi/server_claude_sanitize_test.go`, `internal/httpapi/translator_tools.go`, and this implementation log.
- Behavior/runtime effect: Claude prompt sanitization now routes tool-result cleanup through `claudeProtocolPolicy` entrypoints, the remaining direct tool-result tests now exercise the policy object instead of the package helper, and the old package-level `resolveToolCalls(...)` helper has been removed now that runtime and tests use translator entrypoints. Wire formats and Claude/OpenAI protocol behavior remain unchanged.
- Validation status: in-session LSP diagnostics passed for the touched policy, translator, and test files; broader tests/diff review still pending.
- Open follow-up items: continue hiding the low-risk schema/definition helper cluster (`toolDefName`, `findToolDefByName`, `missingRequiredToolFields`, `toolDefRequiredFields`) behind translator-owned internals, and decide later whether `sanitizeClaudeToolResultText(...)` should also gain a dedicated translator entrypoint or remain an internal policy helper.

- Scope: continued the helper-cluster migration by moving remaining runtime schema-check/tool-call helper usage behind translator entrypoints and updating the last low-risk direct tests away from package-level resolution helpers.
- Files or subsystems touched: `internal/httpapi/translator_openai.go`, `internal/httpapi/claude_policy.go`, `internal/httpapi/server_tool_calls_sse_test.go`, `internal/httpapi/server_claude_sanitize_test.go`, and this implementation log.
- Behavior/runtime effect: Claude policy runtime no longer reaches directly into package-level tool/schema helpers for tool-definition lookup and required-field validation; instead it uses OpenAI-translator entrypoints. The remaining direct tests for tool-call resolution, provider tool-call mapping, and assistant-text sanitization now exercise translator methods rather than package-level helper names. Wire formats and policy behavior remain unchanged.
- Validation status: in-session LSP diagnostics passed for the touched translator, policy, and test files; broader tests/diff review still pending.
- Open follow-up items: decide whether to add translator/policy entrypoints for the remaining intentional direct helper tests (`sanitizeClaudeToolResultText(...)`) or leave them as internal-unit tests while hiding the rest of the tool/schema helper cluster more aggressively later.

- Scope: migrated the next helper cluster by moving OpenAI/Claude runtime and direct tests off package-level tool-call parsing/filtering and Claude tool-mapping helpers toward translator-owned entrypoints.
- Files or subsystems touched: `internal/httpapi/translator_openai.go`, `internal/httpapi/translator_claude.go`, `internal/httpapi/server_tool_calls_sse_test.go`, `internal/httpapi/server_claude_sanitize_test.go`, and this implementation log.
- Behavior/runtime effect: OpenAI tool-call resolution now runs through translator-owned methods instead of a direct package-level `resolveToolCalls(...)` call, Claude tool mapping is now implemented directly on `claudeTranslator`, Claude output normalization now routes tool-call resolution through the OpenAI translator entrypoint, and the first direct helper tests now exercise translator methods instead of package helpers. Wire formats and Claude/OpenAI runtime behavior remain unchanged.
- Validation status: in-session LSP diagnostics passed for the touched translator and test files; broader tests/diff review still pending.
- Open follow-up items: continue moving remaining direct helper usages in `claudePolicy` and additional tests off package-level tool-call/schema helpers so `translator_tools.go` internals can be hidden or removed more safely later.

- Scope: removed the temporary Claude shim wrappers that were left behind during the initial policy extraction and rewired runtime/tests to use the dedicated Claude policy entrypoints directly.
- Files or subsystems touched: `internal/httpapi/translator_claude.go`, `internal/httpapi/claude_policy.go`, `internal/httpapi/server_claude_sanitize_test.go`, and this implementation log.
- Behavior/runtime effect: Claude output normalization no longer routes through a package-level `sanitizeClaudeClientToolCalls(...)` shim, and the Claude sanitize tests now exercise `claudeProtocolPolicy` directly. The old compatibility wrappers for `sanitizeClaudeClientToolCalls(...)` and `Server.sanitizeClaudeMessagesForPrompt(...)` have been removed.
- Validation status: broader tests/diff review still pending.
- Open follow-up items: if desired, continue replacing package-level helper tests with translator/policy object entrypoints so future cleanup can remove more helper leakage safely.

- Scope: completed the next CLIProxyAPI-parity cleanup by moving setup-error classification behind translator methods, evolving the shared proxy orchestration seam into a clearer pipeline contract, and extracting Claude mutable sanitize/cache state into a dedicated protocol policy object.
- Files or subsystems touched: `internal/httpapi/protocol_errors.go`, `internal/httpapi/translator_openai.go`, `internal/httpapi/translator_claude.go`, `internal/httpapi/openai_format.go`, `internal/httpapi/claude_format.go`, new `internal/httpapi/claude_policy.go`, `internal/httpapi/server.go`, `internal/httpapi/claude_parse.go`, `internal/httpapi/proxy_execute.go`, `internal/httpapi/server_openai.go`, `internal/httpapi/server_claude.go`, `internal/httpapi/server_claude_sanitize_test.go`, and this implementation log.
- Behavior/runtime effect: OpenAI and Claude setup failures are now classified through translator entrypoints before existing HTTP writers serialize them; the shared execution seam now uses an explicit `proxyPipeline { Plan, Adapter }` contract instead of separate plan/callback bags; and Claude prompt sanitization/session invalid-tool caching now live in a dedicated `claudeProtocolPolicy` instance attached to the server rather than raw cache fields and helper methods on `Server` itself. Existing OpenAI/Claude JSON and SSE wire serializers remain unchanged.
- Validation status: in-session LSP diagnostics passed for the touched execution, translator, Claude policy, and handler files; broader tests/diff review still pending.
- Open follow-up items: remove temporary compatibility shims like package-level `sanitizeClaudeClientToolCalls(...)` once direct tests are migrated more fully, and consider promoting the `proxyPipeline` seam into a richer internal execution contract only if real protocol growth justifies it.

- Scope: introduced a shared protocol-agnostic proxy execution pipeline so OpenAI chat, OpenAI responses, and Claude messages all use the same begin/execute/error/finalize lifecycle while keeping their existing wire formatters.
- Files or subsystems touched: `internal/httpapi/proxy_execute.go`, `internal/httpapi/server_openai.go`, `internal/httpapi/server_claude.go`, and this implementation log.
- Behavior/runtime effect: the OpenAI-compatible and Claude-compatible handlers now hand a normalized execution plan plus protocol-specific callbacks into one shared `executeProxyProtocol(...)` path. Audit lifecycle, direct executor invocation, and non-stream/stream orchestration are now unified in one place, while OpenAI/Claude JSON and SSE output functions remain unchanged.
- Validation status: in-session LSP diagnostics passed for `proxy_execute.go`, `server_openai.go`, and `server_claude.go`; broader tests/diff review still pending.
- Open follow-up items: move setup-error classification into protocol translators and consider promoting the new execution-plan/callback seam into a more explicit internal pipeline contract if deeper CLIProxyAPI parity is still desired.

- Scope: removed the last product-surface `api_mode` references so the OpenAI-compatible and Claude-compatible proxy APIs no longer expose a mode toggle or frontend mode state.
- Files or subsystems touched: `internal/httpapi/server_admin.go`, `internal/httpapi/server_test.go`, `internal/httpapi/server_settings_state.go`, `web/src/App.svelte`, `web/src/views/DashboardView.svelte`, and this implementation log.
- Behavior/runtime effect: `/api/settings` no longer returns an `api_mode` field, the admin/frontend no longer carries dead `apiMode` state or dashboard prop plumbing, legacy settings tests now assert the old mode field is absent, and the proxy now presents a single direct-only execution model instead of a switchable API mode concept.
- Validation status: in-session grep/LSP checks passed for the touched backend/frontend files; broader tests/build review still pending.
- Open follow-up items: rebuild checked-in frontend assets and continue removing any remaining dead compatibility wording or stale generated references after source cleanup is fully verified.

- Scope: completed the next translator-method pass by wrapping the remaining OpenAI and Claude protocol helpers behind translator methods and introducing a minimal shared upstream-error classifier interface.
- Files or subsystems touched: `internal/httpapi/translator_openai.go`, `internal/httpapi/translator_claude.go`, `internal/httpapi/openai_format.go`, `internal/httpapi/openai_parse.go`, `internal/httpapi/server_openai.go`, `internal/httpapi/claude_parse.go`, `internal/httpapi/claude_format.go`, `internal/httpapi/server_claude.go`, `internal/httpapi/proxy_executor.go`, and this implementation log.
- Behavior/runtime effect: OpenAI and Claude parse/format flows now route more consistently through translator methods for response-format normalization, prompt construction, upstream error classification, anthropic-version normalization, tool mapping, token-budget guarding, session-key derivation, prompt construction, assistant-text cleanup, and response payload shaping. A minimal shared `upstreamErrorClassifier` interface now exists, but wire formats and sanitization behavior remain unchanged.
- Validation status: in-session LSP diagnostics for the updated translator, parse, format, server, and executor files all passed; broader tests/diff review still pending.
- Open follow-up items: if deeper decoupling is still desired, the next step is to stop calling package-level helper implementations from inside translator methods and move the remaining helper bodies fully behind translator-owned internals.

- Scope: moved OpenAI parse-side normalization onto the OpenAI translator object and introduced an explicit initial Claude translator object for safe object-based protocol wiring.
- Files or subsystems touched: `internal/httpapi/translator_openai.go`, `internal/httpapi/openai_parse.go`, `internal/httpapi/translator_claude.go`, `internal/httpapi/claude_parse.go`, `internal/httpapi/claude_format.go`, and this implementation log.
- Behavior/runtime effect: OpenAI request parsing now routes response-format normalization and prompt/direct-option construction through `openAITranslator`, while Claude parse/format paths now depend on a `claudeTranslator` object for version normalization, tool mapping, response-default application, and output normalization. Wire formats and existing sanitization behavior remain unchanged while translator entrypoints are now used on both parse and format paths.
- Validation status: in-session LSP diagnostics for `translator_openai.go`, `openai_parse.go`, `translator_claude.go`, `claude_parse.go`, and `claude_format.go` passed; broader tests/diff review still pending.
- Open follow-up items: converge remaining package-level prompt helpers behind translator methods and consider promoting the translator objects behind a shared interface only after both protocols are fully object-routed.

- Scope: introduced an explicit initial OpenAI translator object and rewired OpenAI output normalization to depend on translator entrypoints instead of raw package-level helpers.
- Files or subsystems touched: new `internal/httpapi/translator_openai.go`, `internal/httpapi/openai_format.go`, and this implementation log.
- Behavior/runtime effect: OpenAI chat-completions and responses formatting paths now route tool-call resolution, structured-output validation, and response text payload normalization through `openAITranslator`, making the output layer closer to a CLIProxyAPI-style translator architecture without changing existing wire formats or structured-output behavior.
- Validation status: in-session LSP diagnostics for `translator_openai.go` and `openai_format.go` passed; broader tests/diff review still pending.
- Open follow-up items: move OpenAI parse-side prompt/structured-output normalization onto translator entrypoints next, then mirror the same object-based pattern for Claude.

- Scope: extracted shared tool-call normalization into a dedicated translator helper file and added a Claude output-normalization helper so protocol formatting files no longer own as much translation logic.
- Files or subsystems touched: new `internal/httpapi/translator_tools.go`, `internal/httpapi/translator_claude.go`, `internal/httpapi/claude_format.go`, `internal/httpapi/server_transport.go`, and this implementation log.
- Behavior/runtime effect: tool-call parsing, required-argument filtering, tool-definition lookup, prompt tool serialization, tool-argument normalization, and provider tool-call mapping now live in `translator_tools.go` instead of `server_transport.go`; Claude response formatting now uses a shared `normalizeClaudeOutput(...)` path before wire serialization. OpenAI-compatible and Claude-compatible wire formats remain unchanged while output-side translation logic is more centralized.
- Validation status: in-session LSP diagnostics for `translator_tools.go`, `translator_claude.go`, `claude_format.go`, and `server_transport.go` passed; broader tests/diff review still pending.
- Open follow-up items: if closer CLIProxyAPI parity is still desired, the next step is to introduce an explicit translator interface/object so OpenAI and Claude parse/format layers depend on translator entrypoints rather than package-level helpers.

- Scope: extracted an initial translator/helper layer for shared protocol normalization and upstream error mapping so remaining prompt/sanitization logic is no longer concentrated in `server_claude.go`.
- Files or subsystems touched: new `internal/httpapi/protocol_errors.go`, new `internal/httpapi/translator_prompt.go`, new `internal/httpapi/translator_claude.go`, `internal/httpapi/server_claude.go`, and this implementation log.
- Behavior/runtime effect: shared upstream error classification, Claude error responses, OpenAI/Claude prompt construction helpers, Claude response defaults, Claude message/system extraction helpers, and Claude sanitization/token-budget/session-key helpers now live in dedicated translator/helper files. OpenAI-compatible and Claude-compatible wire formats remain unchanged, while protocol normalization is less coupled to the main Claude handler file.
- Validation status: in-session LSP diagnostics for `protocol_errors.go`, `translator_prompt.go`, `translator_claude.go`, `server_claude.go`, `openai_parse.go`, `claude_parse.go`, `openai_format.go`, and `claude_format.go` passed; broader tests/diff review still pending.
- Open follow-up items: if closer CLIProxyAPI parity is still desired, the next step is to evolve these helper files into a more explicit translator abstraction/interface instead of package-level helper functions.

- Scope: completed the direct-api executor extraction so legacy Server-owned direct-api execution and thin execution wrappers were removed in favor of a single executor-owned backend path.
- Files or subsystems touched: `internal/httpapi/proxy_executor.go`, `internal/httpapi/proxy_execute.go`, `internal/httpapi/server.go`, `internal/httpapi/server_openai.go`, `internal/httpapi/openai_format.go`, `internal/httpapi/server_claude.go`, `internal/httpapi/claude_format.go`, `internal/httpapi/directapi.go`, and this implementation log.
- Behavior/runtime effect: OpenAI-compatible and Claude-compatible handlers now call the shared executor directly for streaming and non-stream execution, direct-api account selection/retry/SSE parsing now lives inside `proxy_executor.go`, and `directapi.go` is reduced to shared direct-api types/error helpers instead of owning the execution path. Wire formats are unchanged while legacy execution duplication is removed.
- Validation status: in-session LSP diagnostics for `proxy_executor.go`, `proxy_execute.go`, `directapi.go`, `server.go`, `server_openai.go`, `openai_format.go`, `server_claude.go`, and `claude_format.go` passed; broader tests/diff review still pending.
- Open follow-up items: continue toward CLIProxyAPI parity by extracting translator/format normalization away from remaining Server-scoped helper functions if deeper decoupling is desired.

- Scope: extracted an initial executor layer from the shared proxy execution path so backend dispatch is no longer embedded directly in the protocol handlers' execution wrapper.
- Files or subsystems touched: `internal/httpapi/server.go`, new `internal/httpapi/proxy_executor.go`, `internal/httpapi/proxy_execute.go`, and this implementation log.
- Behavior/runtime effect: OpenAI-compatible and Claude-compatible handlers still preserve the same wire formats, but shared backend execution now flows through a dedicated executor object that owns direct-api versus Codex dispatch while `proxy_execute.go` is reduced toward session lifecycle/orchestration. This moves codexsess closer to the CLIProxyAPI stability pattern without changing protocol payloads.
- Validation status: LSP diagnostics for `server.go`, `proxy_execute.go`, and `proxy_executor.go` passed in-session; broader tests/diff review still pending.
- Open follow-up items: continue extracting direct-api HTTP/SSE backend logic into the executor path and further decouple execution from `Server` methods if pursuing closer CLIProxyAPI parity.

- Scope: removed the Zo provider surface end-to-end so the proxy/dashboard now exposes only the Codex-backed OpenAI-compatible and Claude-compatible APIs.
- Files or subsystems touched: `internal/httpapi/routes_proxy.go`, `internal/httpapi/routes_web.go`, `internal/httpapi/server_admin.go`, `internal/httpapi/server_auth.go`, `internal/httpapi/server_traffic_log.go`, `internal/httpapi/server_settings_state.go`, `internal/config/config.go`, `internal/store/types.go`, `internal/store/store_schema.go`, `internal/store/store_busy_test.go`, deleted Zo-only backend files under `internal/httpapi/`, deleted `internal/service/zo.go`, deleted `internal/store/zo.go`, `web/src/App.svelte`, `web/src/views/ApiEndpointView.svelte`, `web/src/app/appHelpers.js`, `web/src/app/endpointExamples.js`, `web/src/app/endpointExampleSelectors.js`, `web/src/app/settingsHelpers.js`, `web/src/app.css`, `web/vite.config.js`, `README.md`, `README.id.md`, `CHANGELOG.md`, and this implementation log.
- Behavior/runtime effect: all `/zo/v1/*` proxy routes, Zo dashboard key-management endpoints, Zo-related settings payload fields, Zo persistence/schema code paths, and Zo frontend panels/examples have been removed; the product is now documented and presented as Codex-only for OpenAI/Claude compatibility.
- Validation status: code changes applied in-session; final diagnostics and targeted test/build verification still pending.
- Open follow-up items: continue splitting OpenAI and Claude parse/format responsibilities out of the remaining protocol handler files.

- Scope: refactored the OpenAI/Claude-compatible proxy server toward the same architectural split used by CLIProxyAPI without changing the existing net/http stack.
- Files or subsystems touched: `internal/httpapi/server.go`, new route registration files `internal/httpapi/routes_web.go` and `internal/httpapi/routes_proxy.go`, new shared execution layer `internal/httpapi/proxy_execute.go`, `internal/httpapi/server_openai.go`, `internal/httpapi/server_claude.go`, and this implementation log.
- Behavior/runtime effect: server startup now uses a thinner route-registration layer separated into web/admin routes versus proxy API routes, and OpenAI/Claude handlers now share a common execution path for account resolution, backend dispatch, account-header propagation, and audit logging while preserving protocol-specific parsing and wire-format responses.
- Validation status: refactor applied in-session; targeted formatting/diagnostics/tests pending after code changes.
- Open follow-up items: continue splitting protocol parsing/formatting helpers out of `server_openai.go` and `server_claude.go` if a second pass is needed for even closer CLIProxyAPI alignment.

- Scope: synced Codex global MCP server definitions into the global Opencode config.
- Files or subsystems touched: external user config `~/.codex/config.toml` (read-only source), external user config `~/.config/opencode/opencode.json`, backup file `~/.config/opencode/opencode.json.bak-pre-mcp-install`, and this implementation log.
- Behavior/runtime effect: Opencode now has a global `mcp` block seeded from Codex for `steel-browser`, `exa`, `reftools`, `github`, and `playwright`; GitHub auth is mapped to an Opencode header using `Bearer {env:GITHUB_PAT_TOKEN}` so the token is read from environment instead of being embedded directly in the JSON file.
- Validation status: inspected `~/.codex/config.toml`; wrote merged config into `~/.config/opencode/opencode.json`; verified the resulting `mcp` block is present; `python -m json.tool ~/.config/opencode/opencode.json` passed.
- Open follow-up items: ensure `GITHUB_PAT_TOKEN` is exported in the shell/session that launches Opencode; if you also want Codex-only MCPs such as filesystem or git added, they need separate Opencode-compatible definitions because they are not currently present in `~/.codex/config.toml`.

## 2026-04-14

- Scope: enforced unique-email export semantics for the raw token export.
- Files or subsystems touched: `internal/service/accounts_backup.go` and `internal/service/accounts_backup_test.go`.
- Behavior/runtime effect: `/api/accounts/export-tokens` now guarantees at most one exported row per normalized email, keeping the freshest account record for duplicate emails instead of emitting repeated entries.
- Validation status: `rtk timeout 120s go test ./internal/service -run 'TestExportAccountTokens_(ReturnsEmailAndTokens|DeduplicatesByEmail)' -count=1` passed; `rtk timeout 120s go test ./internal/httpapi -run 'TestHandleWebExportAccountTokens_ReturnsJSONDownload' -count=1` passed; `rtk timeout 120s npm run build:web` passed from `web/`.
- Open follow-up items: none.

- Scope: added a dashboard export flow for raw Codex account tokens.
- Files or subsystems touched: `internal/service/accounts.go`, `internal/service/accounts_backup.go`, `internal/service/accounts_backup_test.go`, `internal/httpapi/server.go`, `internal/httpapi/server_accounts.go`, `internal/httpapi/server_test.go`, `web/src/App.svelte`, and `web/src/views/DashboardView.svelte`.
- Behavior/runtime effect: the Accounts dashboard now shows an `Export Tokens` action that downloads every stored Codex account as a JSON array containing only `email`, `access_token`, `refresh_token`, and `id_token`; the backend exposes a dedicated `/api/accounts/export-tokens` download endpoint for that export without changing the existing backup/restore payload contract.
- Validation status: `rtk timeout 120s go test ./internal/service ./internal/httpapi -run 'TestExportAccountTokens_ReturnsEmailAndTokens|TestHandleWebExportAccountTokens_ReturnsJSONDownload' -count=1` passed; `rtk timeout 120s npm run build:web` passed from `web/`.
- Open follow-up items: no frontend dashboard-specific automated test was added because the current web test suite only covers the coding views.

## 2026-04-03

- Scope: added a reusable markdown smoke-test checklist for `/chat` post-merge verification.
- Files or subsystems touched: `docs/chat-smoke-test-checklist.md`.
- Behavior/runtime effect: no product/runtime behavior changed; the repository now includes a documented manual verification template for `/chat` session bootstrap, assistant/exec/subagent streaming, stop handling, disconnect recovery, session switching, and canonical history checks.
- Validation status: template written and reviewed in-session; no automated verification required for this docs-only addition.
- Open follow-up items: update the checklist commands if future `/chat` test file names or verification scope change.

- Scope: unified the first stage of `/chat` live timeline normalization so backend websocket streaming now emits compact timeline rows for assistant, exec, and subagent events, and the frontend consumes those rows ahead of raw-event parsing.
- Files or subsystems touched: `internal/httpapi/server_coding_compact.go`, `internal/httpapi/server_coding_compact_test.go`, `internal/httpapi/server_coding_ws.go`, `internal/httpapi/server_coding_ws_test.go`, `web/src/views/CodingView.svelte`, `web/src/views/coding/CodingView.contract.test.js`, `web/src/views/coding/liveMessagePipeline.js`, and `web/src/views/coding/liveMessagePipeline.test.js`.
- Behavior/runtime effect: `session.stream` now includes a backend-normalized `compact_row` for assistant, exec, and subagent updates; websocket transport `event_seq` stays owned by the session event stream while provider sequence is exposed separately as `source_event_seq`; compact rows are carried only inside the websocket payload body to avoid duplicate public representations; and the frontend now merges those compact rows directly before falling back to raw parsing. This reduces live-vs-reload divergence for the supported row types.
- Validation status: `rtk timeout 120s go test ./internal/httpapi -run 'TestCompactRowMatchesCanonicalProjectionForSingleEvent|TestBuildCompactRowFromChatEvent_|TestHandleWebCodingWS_SessionStreamIncludesCompactRow|TestCodingWS_StreamIncludesSourceIdentity' -count=1` passed; `rtk timeout 120s npm run test:unit -- src/views/coding/CodingView.contract.test.js src/views/coding/liveMessagePipeline.test.js src/lib/coding/messageMerge.test.js` passed from `web/`.
- Open follow-up items: MCP activity and file-operation rows still use the existing frontend raw parsing path and should be moved onto the same backend compact-row contract in a later follow-up.

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
