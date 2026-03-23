# Changelog

All notable changes to this project are documented in this file.

The format follows Keep a Changelog and uses semantic version tags (`vMAJOR.MINOR.PATCH`).

## [Unreleased]

- No changes yet.

## [1.0.3] - 2026-03-22

### Web Coding (`/chat`)
- Added dedicated `/chat` web coding workspace with full-screen chat layout and persisted session history.
- Chat transport on `/chat` uses WebSocket (`/api/coding/ws`) for live event streaming.
- Added workspace-aware new session flow with folder picker and server-side path suggestions.
- Added session route targeting via query parameter (`/chat?id=<session_id>`) for direct open/resume behavior.
- Added coding activity timeline rendering alongside assistant outputs in the web chat UI.
- Added coding slash commands support focused for web workflow (`/status` and `/review`).
- Added skill picker integration to insert `$skill_name` hints directly into the composer.
- Added streaming-focused chat UX updates (in-progress state + progressive assistant output).
- Added runtime session APIs (`/api/coding/sessions`, `/api/coding/messages`, `/api/coding/chat`) and persistent storage tables for coding sessions/messages.
- Added runtime lifecycle metadata for coding sessions (`runtime_mode`, `runtime_status`, `restart_pending`) across coding session APIs.
- Added runtime control APIs for chat orchestration: `PUT /api/coding/sessions/runtime`, `GET /api/coding/runtime/status`, and `POST /api/coding/runtime/restart`.
- Added runtime lifecycle websocket events in `/api/coding/ws` (`runtime_started`, `runtime_status`, `runtime_error`) to improve Codex CLI parity on `/chat`.
- Added deferred runtime-restart behavior when restart is requested during in-flight coding turns (`restart_scheduled` -> restart after turn finishes).
- Added dedicated terminal execution bubbles in `/chat` with click-to-open modal for full command output, built from `activity` + raw Codex JSON stream events.
- Added pagination to `/api/coding/messages` with `limit` + `before_id` cursor and page metadata fields (`has_more`, `oldest_id`, `newest_id`).
- Improved `/chat` scroll UX during running streams: user scroll position is preserved when reading older messages and auto-follow to bottom is disabled until user jumps back.
- Added floating `Jump to latest` button in `/chat` when conversation is scrolled away from bottom.
- Made stale pagination cursors (`before_id`) resolve gracefully as end-of-history instead of returning request errors.
- Fixed `/review` command argument handling so prompted review no longer sends conflicting `--uncommitted` + prompt flags to Codex CLI.

### Added
- Zo API key management (multi-key add/remove, per-key request counters, last-request info, and OpenAI-compatible Zo endpoint support).
- System Logs page with database-backed rotation and log detail viewer for account switch/usage events.
- Added `GET /api/accounts/total` endpoint to efficiently count total stored accounts without loading full records.
- Background usage scheduler now automatically "delists" accounts that return API 401 or suspended (`account_suspended`) errors; delisted accounts are permanently skipped in future background checks until manually refreshed and restored.

### Changed
- Codex CLI Strategy now includes:
  - `round_robin` rotation (automatic periodic switch)
  - `manual` mode with auto-switch when remaining usage is below configured threshold.
- API Workspace refinement: Zo model caching, model mapping list improvements, and cleaner endpoint examples.
- Usage automation and account switching behavior were stabilized for large multi-account pools.
- Runtime bind mode is now simplified: use `CODEXSESS_PUBLIC=true` for public bind (`0.0.0.0:<PORT>`), otherwise local bind (`127.0.0.1:<PORT>`). `CODEXSESS_BIND_ADDR` is no longer used.
- Startup logs now clearly show bind scope (`public` or `local`) to avoid ambiguous network status messages.
- Installer systemd unit now uses `CODEXSESS_PUBLIC=true` for server mode.
- README (`EN`/`ID`) was updated to document `CODEXSESS_PUBLIC` and remove old `CODEXSESS_BIND_ADDR` guidance.
- System Logs filter UI was refined: search/filter row now has proper bottom spacing and full-width control layout.
- Refactored all Dashboard filtering (search, account type, token status, usage availability) from Client-Side Svelte to Server-Side SQL query filters, enabling true global search across all paginated pages.
- Advanced Dashboard sorting algorithm now guarantees that any account reporting under `50%` hourly usage is automatically pushed to the bottom of the active list, while ensuring revoked accounts stay firmly beneath them.
- Dashboard account listing now uses server-side pagination via `GET /api/accounts?page=1&limit=20` to prevent browser crashes and backend memory spikes when managing very large account pools.
- Dashboard account list now sorts Revoked accounts to the very bottom automatically.
- Dashboard sidebar now accurately displays the true total count of all accounts across all pages instead of just the paginated slice length.
- Dashboard account card now gracefully hides usage progress bars when an account is marked as Revoked, displaying the revoked reason/status clearly instead.
- Improved revoked token detection logic during background usage refresh to correctly identify API `token_invalidated`, `account_suspended`, and `account_deactivated` errors.
- Added a one-time backend startup migration to automatically scan and backfill the `Revoked` status for any legacy accounts that were suspended prior to the new revoked schema upgrade.

## [1.0.2] - 2026-03-18

### Added
- Dedicated GitHub workflow for PR code review with synthesized final review output.
- Optional PR autofix helper workflow that generates patch suggestions and uploads patch artifacts for manual application.
- New API-key protected endpoint `GET /v1/auth.json` to export the active API account auth payload for external Codex CLI runners.

### Changed
- API Logs endpoint now supports `DELETE /api/logs` to clear captured proxy traffic in one action.
- API Logs web panel now includes a `Clear Logs` button to reset traffic history from the dashboard.
- API Mode selector was moved from Settings into API Workspace so proxy mode control lives with endpoint credentials/config.
- OpenAI `/v1` root payload validation now only dispatches to chat completions and responses APIs.
- API settings payload now exposes `auth_json_endpoint` for automation clients.
- API Workspace UI now shows Auth JSON endpoint and download example for automation use.
- API Workspace UI now shows a dedicated OpenAI Responses endpoint field (`/v1/responses`) with copy action.
- Code-review automation flow is now review-first and can be configured to avoid direct push/merge behavior.
- API logging and automation coverage were refined so review flows stay visible and traceable in API logs.
- Settings and documentation were expanded with automation usage details and cURL examples.
- GitHub Actions code-review and autofix automation are now consolidated into a single workflow file for simpler maintenance.
- GitHub code-review workflow now supports manual run from Actions (`workflow_dispatch`) with optional base/head SHA and PR comment target.
- Release and code-review workflows now use Node 24-ready artifact action (`actions/upload-artifact@v7`), and release publishing was migrated from `softprops/action-gh-release` to `gh release` CLI.
- Fixed Linux installer cleanup trap to avoid `bash: line 1: tmp: unbound variable` after package install in strict shell mode (`set -u`).
- Installer now prints default auth credentials (`admin / hijilabs`) and password change command (`codexsess --changepassword`) after install.
- Server-mode installer now verifies `codexsess.service` active state and prints status check command (`systemctl status codexsess`).
- Default bind address is now `0.0.0.0:<PORT>` (with `CODEXSESS_BIND_ADDR` override support), and server service unit sets `CODEXSESS_BIND_ADDR=0.0.0.0:3061`.
- Installer terminal output now uses clearer colored status lines for info, success, and error messages.
- README now documents `CODEXSESS_BIND_ADDR` and includes GUI-mode `~/.bashrc` bind override example for `0.0.0.0`.
- Startup logging now prints actual bind address separately from local browser URL so public bind mode is explicit in runtime logs.
- Startup logging now adds explicit `public bind enabled` line when bind host is `0.0.0.0` or `::`.
- API request routing now applies backend auto-switch consistently across chat/responses/messages routes: if active account quota is exhausted, it switches to the best available account; if all are exhausted, it returns quota exhaustion.
- First-account bootstrap now auto-selects the very first added account as both API active and CLI (Codex) active when account storage is empty.
- Copy actions now include a non-secure-context fallback (`execCommand`) so copy buttons keep working when accessing CodexSess via IP over HTTP.
- Installer now enforces Codex CLI availability: it auto-installs `npm` when missing and then installs `@openai/codex` globally when `codex` is not found.
- Runtime now performs fail-fast Codex CLI checks (`PATH` + executable test via `codex --version`) before starting the server.
- Added configurable Codex binary resolution (`codex_bin` in config / `CODEXSESS_CODEX_BIN` env), and runtime now resolves/stores absolute binary path for all Codex exec calls.
- Startup Codex binary detection now includes explicit Windows fallback resolution (`codex.cmd` / `.exe` / `.bat`) and exposes detected Codex CLI version in web settings payload.
- Sidebar header now displays `Codex CLI` version directly under `Codex Account Management`.
- Installer now performs a final `systemctl restart codexsess.service` pass at the end of execution (including `--mode update`) whenever the service exists, with explicit status output.
- Installer-generated systemd unit now runs as the installer user by default (`$SUDO_USER` or current user), and sets matching `HOME`/`CODEX_HOME` for consistent Codex account context.
- Proxy API auth execution now uses isolated per-account `CODEX_HOME` under CodexSess storage, so API traffic no longer conflicts with `Use CLI` auth context.
- API account resolution no longer synchronizes CLI active context implicitly; only explicit `Use CLI` updates CLI auth state.
- Codex exec error reporting now prefers structured JSON error events (`error` / `turn.failed`) before falling back to `stderr`/exit code, reducing generic `exit status 1` responses.
- Installer update/download flow now forces release asset re-download with cache-bypass headers/query, so update keeps fetching binary/package even when version tag is unchanged.
- GitHub code-review workflow now supports true manual runs without `pr_number` (via `target_ref`) and auto-creates a dedicated autofix branch on manual mode when safe changes exist.
- GitHub code-review autofix push is now guarded for PR mode to skip direct push on fork-based PRs, preventing workflow failure from cross-repo push permission errors.
- GitHub code-review workflow now runs Codex in CI with `--dangerously-bypass-approvals-and-sandbox` to avoid bubblewrap (`bwrap`) sandbox failures on hosted runners.
- GitHub code-review workflow now provisions default MCP servers in CI (`filesystem`, `sequential_thinking`, `memory`) and enables `exa` when optional `EXA_API_KEY` secret is set.
- GitHub code-review workflow now installs security tooling in CI (`semgrep`, `gitleaks`, `gosec`, `govulncheck`) so Codex can run direct security checks during review/autofix when needed.
- GitHub code-review autofix commit step now strips CI scratch artifacts and excludes `.github/workflows/*` from automation commits to avoid push rejections when workflow-token lacks `workflows` permission.
- GitHub code-review PR comments now prioritize analysis output (review findings + autofix summary) and strip raw code blocks from posted comment content.

## [1.0.1] - 2026-03-18

### Added
- About view in the web console with app information, version state, release link, and latest changelog panel.
- API traffic logging now includes resolved account detail fields.
- Version/update API surface for frontend (`/api/version/check` and version fields in settings response).
- Browser login now supports manual callback URL submission (`/api/auth/browser/complete`) for VPS/remote login flows.

### Changed
- Sidebar now shows current app version above logout.
- Browser document title now defaults to `CodexSess Console` and changes per active menu (for example `Settings - CodexSess Console`).
- About page content is now clearer and includes a dedicated HIJINetwork promotion section with direct external link.
- About page changelog section title now includes version context (for example `Latest Changelog v1.0.1`).
- About page product description was expanded to better explain operational value and production use cases.
- Mobile layout now keeps sidebar behavior with a burger-triggered slide-in menu and overlay close interaction.
- Improved API/logging behavior for streaming and full body capture.
- Improved settings UX and update status visibility.
- Browser login modal now includes manual callback URL input with inline submit action.
- OAuth callback base URL resolution now respects request/forwarded host instead of forcing localhost.
- OpenAI streaming final chunk now sends assistant role in `delta` for stricter client compatibility.
- OpenAI `chat/completions` streaming now emits structured `tool_calls` delta chunks (including `index`, `id`, `function.name`, `function.arguments`) and ends with `finish_reason: "tool_calls"` when tool invocation is selected.
- OpenAI `chat/completions` streaming now filters Codex activity events from output stream and only forwards assistant text deltas for better client compatibility.
- OpenAI `/v1/responses` now supports tool-aware request/response flow: request `tools` + `tool_choice`, non-stream `output.type=function_call`, and stream events for `response.output_item.*` plus `response.function_call_arguments.*`.
- Responses prompt extraction now preserves prior `function_call` and `function_call_output` items to keep tool-loop context across turns.
- Responses API compatibility was tightened for OpenCode parser requirements: added `created_at` on `/v1/responses` payloads and `item_id` + message item lifecycle events on streaming text deltas.
- Tool schema handling now accepts both OpenAI Chat-style (`function.name`) and OpenAI Responses-style (`name`/`parameters`) definitions, improving cross-client compatibility with OpenCode tool requests.
- Responses streaming text flow now emits `response.output_text.done` before item completion for broader OpenAI Responses event compatibility.
- Non-stream `/v1/responses` assistant `output_text` entries now include explicit `annotations` arrays to satisfy stricter OpenAI Responses schema consumers.
- Direct API SSE parsing now extracts native `function_call` items from `response.completed.output` and prioritizes those tool calls over text heuristics for OpenAI-compatible tool-loop responses.
- Codex CLI (`codex exec --json`) parsing now extracts native tool/function calls from `item.*` and `response.*` events, carries them in provider results, and reuses them in `/v1/chat/completions` + `/v1/responses` before text-based fallback parsing.
- Responses streaming now emits both `response.completed` and `response.done` final events for broader OpenCode/plugin parser compatibility.
- Direct API SSE parser now accepts both `response.completed` and `response.done` as finalization events, improving compatibility with varied Responses stream emitters.
- Responses endpoint audit records now correctly persist the request stream mode (`stream=true|false`) for accurate observability.
- Codex CLI native tool-call extraction now requires explicit tool/function item types, preventing false-positive tool-call detection from generic event payloads.
- Tool-call text fallback parser now accepts additional OpenCode-shaped outputs (`tool_calls` as object and concatenated JSON objects), preventing raw tool-call JSON from leaking to chat text output.
- Streaming tool mode now emits periodic SSE keep-alive comments (configurable via `CODEXSESS_SSE_KEEPALIVE_SECONDS`) to prevent idle client timeouts during buffered tool-call resolution.
- Direct API upstream timeout was increased and made configurable via `CODEXSESS_DIRECT_API_TIMEOUT_SECONDS` (bounded to 30-600s) to reduce premature `context deadline exceeded` failures on long responses.
- Streaming responses now set `X-Accel-Buffering: no` to reduce reverse-proxy buffering-induced idle timeouts on SSE clients.
- Backend now runs usage auto-switch checks every 5 minutes for both API active account and CLI active account, and automatically switches to the best account when remaining quota is at/below configured auto-switch threshold.
- Direct API mode now performs automatic account switch + one-time retry when upstream returns HTTP `429`, reducing hard-fail rate limit errors for OpenCode/Codex clients.
- Settings now include backend scheduler control (`usage_scheduler_enabled`) so Background Auto Refresh Usage is replaced by backend-driven usage autoswitch scheduling.
- Backend usage scheduler now runs progressive checks in batches (max 3 accounts per loop) to reduce refresh pressure on large account pools.
- Direct API settings now include account strategy (`direct_api_strategy`) with `round_robin` and `load_balance` options for multi-account routing behavior.
- Settings UI now hides `Usage Auto-Switch Threshold` controls when `Background Auto Switch Scheduler` is disabled, so threshold tuning only appears when scheduler logic is active.
- Settings UI usage automation copy was simplified to backend-scheduler status only, removing stale manual-refresh indicators that could misreport scheduler activity.
- Dashboard now keeps both `Use API` and `Use CLI` actions visible in `direct_api` mode, so manual API/CLI context switching remains available.
- Dashboard account actions now use icon-only buttons for `Refresh` and `Remove`, while `Use API` and `Use CLI` remain text buttons for clearer mode-switch intent.
- Dashboard account list now supports usage filters (`Exhausted (Weekly or 5h)` and `Has Remaining Usage`) and prioritizes `API ACTIVE`/`CODEX ACTIVE` accounts at the top of the list.
- Dashboard filters layout now keeps `All Account Types` and `All Usage` side-by-side on desktop/tablet for faster filtering, and stacks only on small mobile screens.
- Added full account backup/restore flow: backend endpoints for exporting all managed accounts (tokens + usage snapshot metadata) and restoring from JSON backup, with Dashboard actions for download/upload.
- Backup restore now validates backup version and hardens active-account fallback selection, reducing partial-failure risk from malformed/inconsistent backup payloads.
- Backend auto-switch synchronization was refactored to remove shared scheduler/request mutex contention, reducing Direct API request latency spikes during 5-minute background loops.
- Direct API account selection is now more fault-tolerant: `round_robin` and `load_balance` both continue trying other candidates when one account fails, reducing hard-fail requests in multi-account pools.
- Direct API `load_balance` now falls back to `round_robin` when usage snapshots are stale/missing or candidates fail, preventing sticky single-account routing and improving resilience when scheduler data is cold.
- GitHub code-review workflow now uses `jq --rawfile` for large diffs and adds retry/timeout hardening for CodexSess API calls.
- OpenAI Responses streaming now follows strict standard event shape by removing non-standard `response.done` emissions and finalizing streams with `response.completed` + `[DONE]`.
- OpenAI Responses objects now include `output_text` in both stream-final and non-stream `/v1/responses` payloads for stricter SDK compatibility.
- OpenAI `chat/completions` streaming chunks now always serialize `usage` explicitly as `null` or object, avoiding schema drift on strict stream parsers.
- OpenAI Responses streaming now preserves a stable `created_at` across `response.created` and `response.completed` events for the same response ID.
- OpenAI Responses non-tool streaming now falls back to accumulated streamed deltas when provider final text is empty, preventing blank `response.output_text.done` text.
- OpenAI `/v1/responses` streaming now emits data-only SSE frames (without `event:` headers) to better match strict OpenAI-compatible stream parsers.
- OpenAI `/v1/responses` streaming event payloads now drop non-standard `response_id` fields so strict schema validators no longer reject frames.
