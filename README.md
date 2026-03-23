# CodexSess Console

<div align="center">
  <img src="./codexsess.png" alt="CodexSess Logo" width="120" height="120">

  <h3>Web-First Control Plane for Codex Account Operations</h3>
  <p>Manage multi-account API/CLI routing, usage-aware automation, and OpenAI-compatible proxy endpoints in one binary.</p>

  <p>
    <a href="https://github.com/rickicode/CodexSess/releases/latest">
      <img src="https://img.shields.io/github/v/release/rickicode/CodexSess?style=flat-square" alt="Latest Release">
    </a>
    <img src="https://img.shields.io/badge/Backend-Go-00ADD8?style=flat-square" alt="Go">
    <img src="https://img.shields.io/badge/Frontend-Svelte-FF3E00?style=flat-square" alt="Svelte">
    <img src="https://img.shields.io/badge/Mode-Web--First-2f855a?style=flat-square" alt="Web First">
    <img src="https://img.shields.io/badge/Platform-Linux%20%7C%20Windows-3b82f6?style=flat-square" alt="Platform">
  </p>

  <p>
    <a href="./README.md"><strong>English</strong></a> |
    <a href="./README.id.md">Bahasa Indonesia</a>
  </p>

  <p>
    <a href="#overview">Overview</a> •
    <a href="#latest-major-updates">Latest Major Updates</a> •
    <a href="#installation-linux">Installation</a> •
    <a href="#core-capabilities">Core Capabilities</a> •
    <a href="#web-coding-workspace-chat">Web Coding Workspace</a> •
    <a href="#github-code-review-workflow">GitHub Code Review Workflow</a> •
    <a href="#todo">TODO</a> •
    <a href="#ui-preview">UI Preview</a> •
    <a href="#authentication--session">Authentication</a> •
    <a href="#environment-variables">Environment</a>
  </p>
</div>

## Overview

CodexSess is a web-first account gateway for Codex/OpenAI usage.

It is designed for operators who need:

- fast account switching
- separate active account targets for API and Codex CLI
- usage-aware automation (alert + auto switch)
- OpenAI-compatible API surface in production-friendly form

For normal usage, download binaries/packages from the latest release page:

- https://github.com/rickicode/CodexSess/releases/latest

## Latest Major Updates

- New `/chat` coding workspace:
  - persistent sessions and message history
  - workspace picker + path suggestions
  - WebSocket-based live activity stream (`/api/coding/ws`) with auto reconnect and connection status indicator (`[WS Connected/Connecting/Disconnected]`)
  - slash commands (`/status`, `/review`) and skill picker
- Zo OpenAI-compatible integration:
  - multi Zo API key management in dashboard
  - per-key request tracking (`Requests` + `Last request`)
  - cached Zo model list and mapping support
- Direct API and routing automation upgrades:
  - direct API mode improvements for OpenAI-compatible + Claude-compatible clients
  - codex CLI strategy supports `round_robin` (scheduled rotation) and `manual` (threshold-based switch)
  - default auto-switch threshold set to `15%`
  - backend scheduler handles periodic usage checks and active-account fallback
- New system observability:
  - System Logs page with DB-backed log rotation
  - clearer usage refresh source and CLI/API switch logs

## Why CodexSess Exists

CodexSess was built to simplify multi-account Codex operations without splitting tools.

Instead of juggling scripts, manual token edits, and separate dashboards, CodexSess centralizes:

- active API account control
- active CLI account control
- account usage visibility
- automated fallback decisions when limits are low

## Installation (Linux)

Use installer from repository raw script:

```bash
curl -fsSL https://raw.githubusercontent.com/rickicode/CodexSess/main/scripts/install.sh | bash
```

Mode examples:

```bash
# auto (default)
curl -fsSL https://raw.githubusercontent.com/rickicode/CodexSess/main/scripts/install.sh | bash -s -- --mode auto

# gui package install (.deb/.rpm)
curl -fsSL https://raw.githubusercontent.com/rickicode/CodexSess/main/scripts/install.sh | bash -s -- --mode gui

# server / cli install
curl -fsSL https://raw.githubusercontent.com/rickicode/CodexSess/main/scripts/install.sh | bash -s -- --mode server

# update existing install type (auto-detect gui/server)
curl -fsSL https://raw.githubusercontent.com/rickicode/CodexSess/main/scripts/install.sh | bash -s -- --mode update
```

GUI mode public access toggle (via `~/.bashrc`):

```bash
echo 'export CODEXSESS_PUBLIC=true' >> ~/.bashrc
source ~/.bashrc
```

Then restart CodexSess GUI session.

Windows installation:

- Download `.exe` directly from:
  - https://github.com/rickicode/CodexSess/releases/latest

## Core Capabilities

- OpenAI-compatible and Claude-compatible proxy endpoints:
  - `POST /v1/chat/completions` (including SSE streaming)
  - `GET /v1/models`
  - `POST /v1/responses`
  - `POST /claude/v1/messages`
- Separate active account state:
  - API active account
  - CLI (Codex) active account
- Multi-account routing strategy:
  - CLI `round_robin` rotation (default scheduler interval: 5 minutes)
  - CLI `manual` auto-switch when remaining usage is below threshold
- Usage refresh and automation:
  - threshold alerts
  - configurable auto-switch behavior
  - default auto-switch threshold is 15% (configurable in Settings/API)
- Zo API key operations:
  - add/remove multiple Zo API keys
  - monitor request count per key
  - use Zo model list for mapping
- Browser coding workspace:
  - `/chat` sessions with persisted context and activity timeline
- System observability:
  - System Logs view with automatic log rotation
- Embedded web console and API proxy in one binary

## Web Coding Workspace (`/chat`)

`/chat` is the coding area in CodexSess.

In simple terms, this is where you talk to Codex from the browser instead of opening Codex CLI manually.
You create a session, choose your workspace folder, then continue coding in that same session.
Your chat history and workspace context are saved, so you can leave and come back without losing the flow.

The experience is designed to feel practical for daily work:

- You can resume existing sessions from the session list.
- New session flow includes workspace picker with path suggestions.
- Assistant responses and activity updates are streamed in real time over WebSocket (`/api/coding/ws`).
- The status line shows runtime connection state (`[WS Connected]`, `[WS Connecting]`, `[WS Disconnected]`) and reconnects automatically when connection drops.
- Helpful commands like `/status` and `/review` are available in chat.
- Skill hints can be inserted quickly using `$skill_name`.

This makes CodexSess useful as a browser-based coding companion for desktop and remote/mobile access while keeping session context organized.

## UI Preview

<table>
  <tr>
    <td align="center">
      <img src="./screenshots/dashboard.png" alt="Dashboard" width="420">
    </td>
    <td align="center">
      <img src="./screenshots/settings.png" alt="Settings" width="420">
    </td>
  </tr>
  <tr>
    <td align="center">
      <img src="./screenshots/chat.png" alt="Chat Workspace" width="420">
    </td>
    <td align="center">
      <img src="./screenshots/workspaces.png" alt="Workspaces" width="420">
    </td>
  </tr>
  <tr>
    <td align="center">
      <img src="./screenshots/api-activity.png" alt="API Activity" width="420">
    </td>
    <td align="center">
      <img src="./screenshots/system-logs.png" alt="System Logs" width="420">
    </td>
  </tr>
</table>

## Authentication & Session

- Management console requires login.
- First-run default credential:
  - username: `admin`
  - password: `hijilabs`
- Session remember duration: 30 days.
- Password change via CLI:
  - `--changepassword`

API compatibility routes under `/v1/*` and `/claude/v1/*` remain API-key style routes and are not blocked by web login UI flow.
This means OpenAI clients and Claude-style clients can both be routed through CodexSess.

## GitHub Code Review Workflow

To use CodexSess automated PR review/autofix in GitHub:

- Use workflow file: `.github/workflows/code-review.yml`
- Add required repository secrets:
  - `CODEXSESS_URL`
  - `CODEXSESS_API_KEY`
- Optional secret:
  - `EXA_API_KEY` (enables Exa MCP server in workflow)
- Trigger modes:
  - automatic on `pull_request` events
  - manual via `workflow_dispatch`
- Manual inputs (`workflow_dispatch`):
  - `target_ref` (optional, branch/tag/sha; default `main`)
  - `review_scope` (`diff` or `full`)
  - `review_focus` (optional focus area)

Behavior:

- Automatic PR run: posts review to PR and pushes autofix to PR branch when allowed.
- Manual run: reviews selected `target_ref`; if autofix exists, workflow creates and pushes a new branch automatically.
- Workflow configures default MCP servers for Codex in CI:
  - `filesystem` (free)
  - `sequential_thinking` (free)
  - `memory` (free)
  - `exa` (enabled when `EXA_API_KEY` is provided)

## TODO

- Continue parity improvements with Codex CLI core flow:
  - start/resume/stop interactive sessions
  - stream tool/terminal output in real time
  - workspace-aware file editing and diffs
  - approval/sandbox controls exposed in web UI
- Implement session isolation and security guardrails before public multi-device usage:
  - per-session workspace boundaries
  - strict command policy and audit logs
  - rate limits and timeout controls

## Environment Variables

| Variable                          | Default       | Example                                                         | Description                                                                                                                                                                             |
| --------------------------------- | ------------- | --------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `PORT`                            | `3061`        | `PORT=8080`                                                     | HTTP server port.                                                                                                                                                                       |
| `CODEXSESS_PUBLIC`                | `false`       | `CODEXSESS_PUBLIC=true`                                         | Enable network/public bind (`0.0.0.0:<PORT>`). When false, bind is local-only (`127.0.0.1:<PORT>`).                                                                                     |
| `CODEXSESS_NO_OPEN_BROWSER`       | `false`       | `CODEXSESS_NO_OPEN_BROWSER=true`                                | Disable automatic browser opening on startup. Truthy values: `1`, `true`, `yes`.                                                                                                        |
| `CODEXSESS_CODEX_SANDBOX`         | `full-access` | `CODEXSESS_CODEX_SANDBOX=full-access`                           | Sandbox mode passed to `codex exec` (`write/workspace-write` is normalized to `full-access`).                                                                                           |
| `CODEXSESS_CLEAN_EXEC`            | `true`        | `CODEXSESS_CLEAN_EXEC=false`                                    | Run Codex execution in isolated mode (`true`) or normal environment (`false`).                                                                                                          |
| `CODEXSESS_CLI_SWITCH_NOTIFY_CMD` | ``            | `CODEXSESS_CLI_SWITCH_NOTIFY_CMD="peon preview resource.limit"` | Optional command executed when CLI active account changes. Env: `CODEXSESS_CLI_SWITCH_FROM`, `CODEXSESS_CLI_SWITCH_TO`, `CODEXSESS_CLI_SWITCH_REASON`, `CODEXSESS_CLI_SWITCH_TO_EMAIL`. |

Notes:

- On Windows, data directory defaults to `%APPDATA%\\codexsess` when `APPDATA` is available.
- `CODEX_HOME` is set internally per selected account and is not intended as an external switch for CodexSess itself.

## Project Scope

CodexSess focuses on operational reliability for Codex account usage:

- predictable account selection
- clear active-state visibility
- usage-aware automation and fallback
- OpenAI-compatible integration surface
