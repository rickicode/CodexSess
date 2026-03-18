# Changelog

All notable changes to this project are documented in this file.

The format follows Keep a Changelog and uses semantic version tags (`vMAJOR.MINOR.PATCH`).

## [1.0.2] - 2026-03-18

### Added
- Dedicated GitHub workflow for PR code review using `POST /v1/code-review` with chunked diff processing and synthesized final review output.
- Optional PR autofix helper workflow that generates patch suggestions and uploads patch artifacts for manual application.

### Changed
- Code-review automation flow is now review-first and can be configured to avoid direct push/merge behavior.
- API/code-review integration and logging coverage were refined so review calls are visible and traceable in API logs.
- Settings and documentation were expanded with code-review endpoint usage details and cURL examples.
- GitHub Actions code-review and autofix automation are now consolidated into a single workflow file for simpler maintenance.
- GitHub code-review workflow now supports manual run from Actions (`workflow_dispatch`) with optional base/head SHA and PR comment target.
- Release and code-review workflows now use Node 24-ready artifact action (`actions/upload-artifact@v7`), and release publishing was migrated from `softprops/action-gh-release` to `gh release` CLI.
- Fixed Linux installer cleanup trap to avoid `bash: line 1: tmp: unbound variable` after package install in strict shell mode (`set -u`).
- Installer now prints default auth credentials (`admin / hijilabs`) and password change command (`codexsess --changepassword`) after install.
- Server-mode installer now verifies `codexsess.service` active state and prints status check command (`systemctl status codexsess`).
- Default bind address is now `0.0.0.0:<PORT>` (with `CODEXSESS_BIND_ADDR` override support), and server service unit sets `CODEXSESS_BIND_ADDR=0.0.0.0:3061`.
- Installer terminal output now uses clearer colored status lines for info, success, and error messages.
- README now documents `CODEXSESS_BIND_ADDR` and includes GUI-mode `~/.bashrc` bind override example for `0.0.0.0`.

## [1.0.1] - 2026-03-18

### Added
- About view in the web console with app information, version state, release link, and latest changelog panel.
- API traffic logging now includes resolved account detail fields.
- Version/update API surface for frontend (`/api/version/check` and version fields in settings response).
- Browser login now supports manual callback URL submission (`/api/auth/browser/complete`) for VPS/remote login flows.
- New API endpoint `POST /v1/code-review` with optional `custom_prompt` for dedicated code-review workflows.

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
- Code review endpoint now enforces input size limits and quota checks for consistency with other proxy endpoints.
- GitHub code-review workflow now uses `jq --rawfile` for large diffs and adds retry/timeout hardening for CodexSess API calls.
