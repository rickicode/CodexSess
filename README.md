# codexsess

OpenAI-compatible wrapper for Codex with:
- `POST /v1/chat/completions` (+streaming SSE)
- `GET /v1/models`
- `POST /v1/responses` (non-stream)
- Multi-account management
- Usage refresh from `chatgpt.com/backend-api/wham/usage`
- Web-first SPA console (embedded) in a single Go binary
- Dedicated auth-account storage directory (cockpit-style switching)

## Build

```bash
go build -o codexsess .
```

Or use:

```bash
make run
```

`make run` will:
- build Svelte SPA frontend from `web/`
- build Go binary
- start `codexsess`

Development mode:

```bash
make dev
```

This runs:
- Svelte dev server on `http://127.0.0.1:3051/`
- Go backend with `air` on `http://127.0.0.1:3052`

Windows build with default icon:

```bash
make build-windows
```

## First run

```bash
./codexsess
```

Supported CLI:
- `--changepassword` to change admin web-console credential.
No `.env` file is used.

This generates:
- Linux/macOS:
  - `~/.codexsess/config.yaml`
  - `~/.codexsess/master.key`
  - `~/.codexsess/data.db`
  - `~/.codexsess/auth-accounts/` (per-account auth data)
- Windows:
  - `%APPDATA%/codexsess/config.yaml`
  - `%APPDATA%/codexsess/master.key`
  - `%APPDATA%/codexsess/data.db`
  - `%APPDATA%/codexsess/auth-accounts/`

Default web-console auth:
- username: `admin`
- password: `hijilabs`

Change password:

```bash
./codexsess --changepassword
```

Default server/web port is `3061`.
You can override it with environment variable:

```bash
PORT=3062 ./codexsess
```

## Web-only operations

Use `http://127.0.0.1:3061/`:
- import account token JSON
- list all accounts
- switch account (`use`)
- refresh usage
- remove account
- auth session cookie remembers login for 30 days

## Run

```bash
./codexsess
```

This starts server + SPA web console.  
Use `codexsess_api_key` from `~/.codexsess/config.yaml`.  
Web console is available at `http://127.0.0.1:3061/` by default.

### Chat completions example

```bash
curl http://127.0.0.1:3061/v1/chat/completions \
  -H "Authorization: Bearer sk-..." \
  -H "X-Codex-Account: <id|email|alias>" \
  -H "Content-Type: application/json" \
  -d '{
    "model":"gpt-5",
    "messages":[{"role":"user","content":"Reply exactly with: OK"}],
    "stream":false
  }'
```

### Models example

```bash
curl http://127.0.0.1:3061/v1/models \
  -H "Authorization: Bearer sk-..."
```

### Responses example

```bash
curl http://127.0.0.1:3061/v1/responses \
  -H "Authorization: Bearer sk-..." \
  -H "X-Codex-Account: <id|email|alias>" \
  -H "Content-Type: application/json" \
  -d '{
    "model":"gpt-5",
    "input":"Reply exactly with: OK",
    "stream":false
  }'
```

## Web Console

Running `./codexsess` starts web mode automatically.

You can:
- list all accounts
- import account token JSON
- switch active account (`use`)
- refresh usage
- remove account

When you switch account via web `use`, `codexsess` syncs that account to `~/.codex/auth.json` so Codex CLI follows the active account.
