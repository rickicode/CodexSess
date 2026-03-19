# Changelog

All notable changes to this project are documented in this file.

The format follows Keep a Changelog and uses semantic version tags (`vMAJOR.MINOR.PATCH`).

## [1.0.2] - 2026-03-18

### Web Coding (`/chat`)
- Added dedicated `/chat` web coding workspace with full-screen chat layout and persisted session history.
- Added workspace-aware new session flow with folder picker and server-side path suggestions.
- Added session route targeting via query parameter (`/chat?id=<session_id>`) for direct open/resume behavior.
- Added coding activity timeline rendering alongside assistant outputs in the web chat UI.
- Added coding slash commands support focused for web workflow (`/status` and `/review`).
- Added skill picker integration to insert `$skill_name` hints directly into the composer.
- Added streaming-focused chat UX updates (in-progress state + progressive assistant output).
- Updated README explanation for `/chat` in user-facing language (clear workflow and practical usage context).

### Added
- Dedicated GitHub workflow for PR code review with synthesized final review output.
- Optional PR autofix helper workflow that generates patch suggestions and uploads patch artifacts for manual application.
- New API-key protected endpoint `GET /v1/auth.json` to export the active API account auth payload for external Codex CLI runners.
- Web coding runtime session APIs (`/api/coding/sessions`, `/api/coding/messages`, `/api/coding/chat`) for ChatGPT-style session list and message history.
- Persistent storage tables for coding sessions and coding messages in the local SQLite store.

### Changed
- OpenAI `/v1` root payload validation now only dispatches to chat completions and responses APIs.
- API settings payload now exposes `auth_json_endpoint` for automation clients.
- API Workspace UI now shows Auth JSON endpoint and download example for automation use.
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
- Sidebar navigation now includes a dedicated `Sessions` menu and opens to that page by default.
- New `Sessions` web UI provides first-open auto session creation, left-side session list, and coding chat composer/messages in a desktop app layout.
- Codex executor now supports explicit `workdir` options so web coding sessions can run in project context while keeping `CODEX_HOME` auth separation.
- `Sessions` flow now opens a workspace/folder picker modal on `New Session` with default path `~/`.
- Added path suggestion API (`/api/coding/path-suggestions`) and UI autocomplete that suggests directories from server-side listing (`ls`-style).
- Coding session metadata now persists `work_dir`, and active path is shown directly in the chat panel.
- Sessions/chat now uses dedicated route `/chat` for full coding layout.
- `/chat` view now hides dashboard shell elements (sidebar, status banner, API/CLI summary) and focuses only on coding UI.
- `Sessions` navigation button now redirects to `/chat`, and `/chat` provides an explicit `Back to Dashboard` action.
- `/chat` layout height was rebalanced to avoid whole-page scrolling; only the coding message area is scrollable.
- Workspace path visibility in chat was improved (`Workspace Path` bar), and `New Session` workspace picker modal visibility was hardened.
- Chat composer now supports slash commands (`/help`, `/model`, `/path`, `/new`) for quick runtime control without leaving chat.
- Chat composer now supports `$skill` markers, which are passed as skill hints into the request payload.
- Send button in coding chat was redesigned for clearer action state and better visual affordance.
- Added `/status` slash command to quickly show current session/model/path context in the chat status line.
- Added dedicated Skill Picker modal (with search) to insert available `$skill_name` tokens into composer directly from UI.
- Added backend skills listing endpoint (`/api/coding/skills`) that discovers installed skills from local Codex/agents skill directories.
- Fixed coding session persistence flow so user messages are only stored after successful Codex response, preventing duplicate history on retry after upstream failures.
- `$skill` handling now preserves original user prompt text (no token stripping) while still passing skill hints.
- Added coding session preferences update API (`PUT /api/coding/sessions`) so model/path changes persist immediately on active session.
- Model dropdown and `/model` + `/path` commands now auto-save session preferences (debounced), reducing state drift between UI and stored session.
- Skills discovery now supports custom roots via `CODEXSESS_SKILL_DIRS` (path-list env) in addition to default local skill directories.
- Chat header controls were reorganized: model dropdown + `New Session` + `Delete` now live in the topbar only for cleaner layout.
- Session list is now accessible via a dedicated `Sessions` sidebar modal drawer, including full session selection flow.

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
- GitHub code-review workflow now uses `jq --rawfile` for large diffs and adds retry/timeout hardening for CodexSess API calls.
