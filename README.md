# tourplannerbot

Telegram bot for Diana, a licensed Barcelona tour guide. Internal tool to plan trips and answer quick queries using OpenAI.

## Features

- PIN-based authorization persisted in PostgreSQL
- Conversation history isolated by Telegram chat and topic
- Config loaded from environment variables through Viper
- Native tool registry and bounded LLM tool-calling loop
- Dynamic MCP tool discovery over Streamable HTTP
- Persisted tool calls and results
- `current_time` tool with a configurable default IANA timezone
- Local Wikipedia and OpenStreetMap MCP support with optional bearer authentication
- Live Telegram typing and an editable thinking/tool-use progress message
- Native Telegram Rich Message tables with a readable list fallback
- Structured JSON logging via `slog`
- Graceful shutdown on SIGTERM
- Echo HTTP server with liveness (`/healthz`) and PostgreSQL readiness (`/readyz`) checks

The bot stores authorized user messages, generated replies, tool calls, and tool results, then sends the newest items from the same Telegram chat and topic to the LLM as context. A non-topic chat uses `message_thread_id = 0`.

## Project Structure

```
cmd/bot/main.go               # Entrypoint
internal/
  config/config.go            # Env var loading
  database/database.go        # GORM PostgreSQL connection
  webapp/                     # Echo server and embedded Mini App assets
  llm/client.go               # Official OpenAI Go SDK Responses API client
  models/                      # GORM models for authorized users and messages
  telegram/handler.go         # Message routing and persisted LLM/tool loop
  telegram/progress.go        # Typing and editable response progress
  tools/
    registry.go               # Shared native/MCP-ready tool registry
    types.go                  # Provider-independent tool contract
    currenttime/              # Native current_time tool
    mcpclient/                # Streamable HTTP MCP adapter
migrations/                   # Goose SQL migrations
prompts/
  system_trip.md              # System prompt for group/trip chats
  system_query.md             # System prompt for private/query chats
  skills/                     # Skill knowledge files (BCN monuments, etc.)
Dockerfile
docker-compose.yml            # Local dev: bot + postgres
.env.example
web/                           # SolidJS + TypeScript + Vite Mini App source
```

## Running Locally

1. Copy and fill in the env file:
   ```bash
   cp .env.example .env
   # edit .env with your values
   ```

2. Start with Docker Compose:
   ```bash
   docker compose up --build
   ```

3. Or run directly (the process keeps `/healthz` available while PostgreSQL reconnects):
   ```bash
   make run
   ```

### HTTPS tunnel for Telegram Mini Apps

The health endpoint can be exposed through a temporary HTTPS tunnel before the
Mini App UI is added. Start the bot, then in another terminal run:

```bash
ngrok http 8080
```

Use the generated HTTPS address as `APP_BASE_URL` when a later Mini App slice
needs to generate Telegram links. Until then, `APP_BASE_URL` may be unset.

The MCP URLs in `.env.example` target servers published on the host. When the
bot itself runs inside Docker Desktop, use `host.docker.internal` instead of
`127.0.0.1`, or attach all services to one Compose network and use their service
names.

## Mini App Frontend

The production Mini App is compiled from `web/` and embedded in the Go binary;
Node is not required at runtime. Build the frontend and binary together with:

```bash
make build
```

After the bot starts, open `http://localhost:8080/miniapp` to see the shell.
It reports a clear development state outside Telegram, and uses Telegram theme,
viewport, and safe-area values when the SDK is available.

For frontend hot-module replacement, run Vite separately. It listens on 5173
and proxies `/api` to the Go process on 8080:

```bash
npm --prefix web run dev
```

Use an HTTPS tunnel to port 5173 for Telegram browser testing in this mode;
use a tunnel to port 8080 when testing the compiled production shell.

## Database Migrations

Migrations are SQL files in `migrations/` and are applied with the standalone [Goose](https://github.com/pressly/goose) binary, never by the bot process.

For local development, start PostgreSQL, install Goose once, then apply the migrations:

```bash
make db-up
make goose-install
make migrate-up
```

Use `make migrate-status` to inspect the applied versions. `DATABASE_URL` must point to the local database; `.env.example` contains the default Docker Compose URL.

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `TELEGRAM_BOT_TOKEN` | yes | — | Bot token from @BotFather |
| `DATABASE_URL` | yes | — | PostgreSQL connection URL |
| `OPENAI_API_KEY` | yes | — | OpenAI (or compatible) API key |
| `ACCESS_PIN` | yes | — | PIN users must enter to unlock the bot |
| `PORT` | no | `8080` | HTTP port; production listens on the platform-provided value |
| `APP_BASE_URL` | no | — | Public HTTPS URL, such as the final service domain or a temporary tunnel |
| `OPENAI_MODEL` | no | `gpt-5.5` | Model name |
| `OPENAI_BASE_URL` | no | `https://api.openai.com/v1` | OpenAI Responses API base URL |
| `LLM_PROVIDER` | no | `openai` | Provider label written to LLM audit records |
| `LLM_TARIFFS_DIR` | no | `tariffs` | Versioned CSV directory used to price the configured model |
| `LLM_MAX_TOKENS` | no | `2048` | Max tokens per LLM response |
| `LLM_HISTORY_MAX_MESSAGES` | no | `20` | Newest messages included from the current chat/topic |
| `TOOL_CALL_MAX_ITERATIONS` | no | `10` | Max tool-calling loop iterations |
| `TOOLS_CURRENT_TIME_ENABLED` | no | `true` | Registers the native `current_time` tool |
| `TOOLS_CURRENT_TIME_DEFAULT_TIMEZONE` | no | `Europe/Madrid` | Default IANA timezone used when a call omits `timezone` |
| `TOOLS_MCPS` | no | empty | Comma-separated MCP server names; list membership enables a server by default |
| `TOOLS_<NAME>_ENABLED` | no | list membership | Explicit per-server override; `true` may enable an unlisted known server and `false` disables a listed one |
| `TOOLS_<NAME>_URL` | when enabled | — | Absolute Streamable HTTP MCP endpoint |
| `TOOLS_<NAME>_AUTH_TYPE` | no | `none` | `none` or `bearer` |
| `TOOLS_<NAME>_TOKEN` | for bearer | — | Bearer token; never logged |
| `TOOLS_<NAME>_TIMEOUT` | no | `30s` | Positive Go duration applied to discovery and each tool call |
| `LOG_LEVEL` | no | `info` | `info` or `debug` |

## Deployment (DigitalOcean App Platform)

- Component type: **Service**, listening on `0.0.0.0:$PORT`; it still runs one Telegram polling consumer
- `GET /healthz` is used for platform health and liveness checks; `GET /readyz` returns `503` until PostgreSQL is reachable
- Set all required env vars as secrets in the DO console
- Push to GitHub → auto-deploy triggers; the GitHub Actions job waits for DigitalOcean App Platform to finish the rollout and fails if it fails
- The GitHub workflow runs migrations in a dedicated job before deploying the service. Set `DO_DATABASE_ID` and `DO_APP_ID` as GitHub Actions variables, and set `ACCESS_PIN` as a GitHub Actions secret. `APP_BASE_URL` will be added to the deployment flow in Slice 3, when Telegram links need it.
