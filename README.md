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

The bot stores authorized user messages, generated replies, tool calls, and tool results, then sends the newest items from the same Telegram chat and topic to the LLM as context. A non-topic chat uses `message_thread_id = 0`.

## Project Structure

```
cmd/bot/main.go               # Entrypoint
internal/
  config/config.go            # Env var loading
  database/database.go        # GORM PostgreSQL connection
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

3. Or run directly (requires a running Postgres):
   ```bash
   go run ./cmd/bot
   ```

The MCP URLs in `.env.example` target servers published on the host. When the
bot itself runs inside Docker Desktop, use `host.docker.internal` instead of
`127.0.0.1`, or attach all services to one Compose network and use their service
names.

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

- Component type: **Worker** (polling, no inbound HTTP)
- Set all required env vars as secrets in the DO console
- Push to GitHub → auto-deploy triggers; the GitHub Actions job waits for DigitalOcean App Platform to finish the rollout and fails if it fails
- The GitHub workflow runs migrations in a dedicated job before deploying the worker. Set `DO_DATABASE_ID` and `DO_APP_ID` as GitHub Actions variables, and set `ACCESS_PIN` as a GitHub Actions secret.
