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

## Get It

Do not build manually for normal usage.
Use the latest published binaries from GitHub Releases:

- Latest release: https://github.com/rickicode/CodexSess/releases/latest

## Project Goal

CodexSess focuses on operational reliability for account-based Codex usage:
clear active-state control, usage-aware switching, and predictable API behavior.
