# CodexSess

[English](./README.md) | [Bahasa Indonesia](./README.id.md)

CodexSess is a web-first account management gateway for Codex/OpenAI usage.
It provides an OpenAI-compatible API surface and a built-in management console in one binary.

## What CodexSess Is

CodexSess is designed to sit between your clients and Codex/OpenAI access tokens.
It helps you manage multiple accounts, choose which account is active for API and CLI flows, monitor usage, and switch quickly when limits are low.

## Why This Project Exists

CodexSess was created to:
- simplify multi-account Codex management in one place
- separate active account selection for API and Codex CLI
- reduce downtime by enabling fast account switching
- provide a practical web console without extra setup complexity
- run in web mode on Linux/Windows with the same operational flow

## Core Capabilities

- OpenAI-compatible endpoints:
  - `POST /v1/chat/completions` (including SSE streaming)
  - `GET /v1/models`
  - `POST /v1/responses`
- Multi-account management UI
- Manual and automated account switching logic
- Usage refresh/monitoring integration
- Single-binary deployment with embedded SPA

## Authentication and Session

- Management console login uses local admin credentials
- Default credential on first run:
  - username: `admin`
  - password: `hijilabs`
- Session is remembered for 30 days via cookie
- Password can be changed via CLI:
  - `--changepassword`

## API Compatibility Scope

- Management routes are protected by web login
- API compatibility routes under `/v1/*` and `/claude/v1/*` keep API-key style access

## Environment Variables

| Variable | Default | Example | Description |
|---|---|---|---|
| `PORT` | `3061` | `PORT=8080` | HTTP server port. Bind address is `127.0.0.1:<PORT>`. |
| `CODEXSESS_NO_OPEN_BROWSER` | `false` (auto-open enabled) | `CODEXSESS_NO_OPEN_BROWSER=true` | Disable automatic browser opening on startup. Truthy values: `1`, `true`, `yes`. |
| `CODEXSESS_CODEX_SANDBOX` | `workspace-write` | `CODEXSESS_CODEX_SANDBOX=read-only` | Sandbox mode passed to `codex exec`. Supported values depend on Codex CLI (for example: `read-only`, `workspace-write`, `danger-full-access`). |
| `CODEXSESS_CLEAN_EXEC` | `true` | `CODEXSESS_CLEAN_EXEC=false` | Run Codex API execution in clean/isolated mode. `true` isolates `HOME`/`XDG_*` and uses ephemeral Codex session; `false` uses normal environment. |

Notes:
- On Windows, data directory defaults to `%APPDATA%\\codexsess` when `APPDATA` is available.
- `CODEX_HOME` is set internally per selected account and is not intended as an external runtime switch for CodexSess itself.

## Get It

Do not build manually for normal usage.
Use the latest published binaries from GitHub Releases:

- Latest release: https://github.com/rickicode/CodexSess/releases/latest

## CLI Installer

Installer script is maintained in this repository (`scripts/install.sh`) and can be executed directly via raw URL.
This installer is Linux-only.
For Windows, download the `.exe` binary directly from the latest release page.

```bash
curl -fsSL https://raw.githubusercontent.com/rickicode/CodexSess/main/scripts/install.sh | bash
```

Mode-specific one-liners:

```bash
# auto (default)
curl -fsSL https://raw.githubusercontent.com/rickicode/CodexSess/main/scripts/install.sh | bash -s -- --mode auto

# gui (linux desktop package)
curl -fsSL https://raw.githubusercontent.com/rickicode/CodexSess/main/scripts/install.sh | bash -s -- --mode gui

# server / cli
curl -fsSL https://raw.githubusercontent.com/rickicode/CodexSess/main/scripts/install.sh | bash -s -- --mode server

# update existing install (auto-detects gui/server)
curl -fsSL https://raw.githubusercontent.com/rickicode/CodexSess/main/scripts/install.sh | bash -s -- --mode update
```

Installer modes:
- `--mode auto` (default): detects desktop/server environment automatically
- `--mode gui`: installs `.deb`/`.rpm` GUI package on Linux
- `--mode server`: downloads and installs release binary directly (CLI/server mode)
- `--mode update`: detects existing install type (`gui`/`server`) and updates it automatically

Examples:
- `bash install.sh --mode gui`
- `bash install.sh --mode server --bin-dir /usr/local/bin`
- `bash install.sh --mode update`
- `bash install.sh --version v1.0.1`

Server mode notes:
- On Linux, server install/update automatically provisions and restarts `codexsess.service` via `systemd`.
- Service runs with `CODEXSESS_NO_OPEN_BROWSER=1` to stay headless.

## Project Goal

CodexSess focuses on operational reliability for account-based Codex usage:
clear active-state control, usage-aware switching, and predictable API behavior.
