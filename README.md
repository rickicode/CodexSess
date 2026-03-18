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
    <a href="#installation-linux">Installation</a> •
    <a href="#core-capabilities">Core Capabilities</a> •
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

GUI mode bind override (via `~/.bashrc`):

```bash
echo 'export CODEXSESS_BIND_ADDR=0.0.0.0:3061' >> ~/.bashrc
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
- Usage refresh and automation:
  - threshold alerts
  - configurable auto-switch behavior
- Embedded web console and API proxy in one binary

## UI Preview

<p align="center">
  <img src="./screenshots/codexsess-dashboard.png" alt="CodexSess Dashboard" width="92%">
</p>

<p align="center">
  <img src="./screenshots/codexsess-settings.png" alt="CodexSess Settings" width="92%">
</p>

<p align="center">
  <img src="./screenshots/codexsess-apilogs.png" alt="CodexSess API Logs" width="92%">
</p>

<p align="center">
  <img src="./screenshots/codexsess-about.png" alt="CodexSess About" width="92%">
</p>

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

- Add a web-based Codex runtime wrapper so coding sessions can be run from mobile/desktop browser with CLI-like behavior.
- Target parity with Codex CLI core flow:
  - start/resume/stop interactive sessions
  - stream tool/terminal output in real time
  - workspace-aware file editing and diffs
  - approval/sandbox controls exposed in web UI
- Implement session isolation and security guardrails before public multi-device usage:
  - per-session workspace boundaries
  - strict command policy and audit logs
  - rate limits and timeout controls

## Environment Variables

| Variable | Default | Example | Description |
|---|---|---|---|
| `PORT` | `3061` | `PORT=8080` | HTTP server port used when `CODEXSESS_BIND_ADDR` is not explicitly set. |
| `CODEXSESS_BIND_ADDR` | `0.0.0.0:<PORT>` | `CODEXSESS_BIND_ADDR=0.0.0.0:3061` | Full bind address override (`host:port`) for HTTP server. |
| `CODEXSESS_NO_OPEN_BROWSER` | `false` | `CODEXSESS_NO_OPEN_BROWSER=true` | Disable automatic browser opening on startup. Truthy values: `1`, `true`, `yes`. |
| `CODEXSESS_CODEX_SANDBOX` | `workspace-write` | `CODEXSESS_CODEX_SANDBOX=read-only` | Sandbox mode passed to `codex exec`. |
| `CODEXSESS_CLEAN_EXEC` | `true` | `CODEXSESS_CLEAN_EXEC=false` | Run Codex execution in isolated mode (`true`) or normal environment (`false`). |

Notes:
- On Windows, data directory defaults to `%APPDATA%\\codexsess` when `APPDATA` is available.
- `CODEX_HOME` is set internally per selected account and is not intended as an external switch for CodexSess itself.

## Project Scope

CodexSess focuses on operational reliability for Codex account usage:
- predictable account selection
- clear active-state visibility
- usage-aware automation and fallback
- OpenAI-compatible integration surface
