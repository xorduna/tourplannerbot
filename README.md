# tourplannerbot

Telegram bot for Diana, a licensed Barcelona tour guide. Internal tool to plan trips and answer quick queries using OpenAI.

## Features (Slice 3 — LLM integration in progress)

- Echoes messages back (foundation for all future slices)
- Config loaded from environment variables
- Structured JSON logging via `slog`
- Graceful shutdown on SIGTERM

Slices 1 and 2 are complete. Slice 3 sends each authorized text message to the LLM independently and returns one joke related to that message.

## Project Structure

```
cmd/bot/main.go               # Entrypoint
internal/
  config/config.go            # Env var loading
  database/database.go        # GORM PostgreSQL connection
  llm/client.go               # Official OpenAI Go SDK Responses API client
  models/allowed_user.go      # GORM model for authorized Telegram users
  telegram/handler.go         # Message routing
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
| `LLM_MAX_TOKENS` | no | `2048` | Max tokens per LLM response |
| `TOOL_CALL_MAX_ITERATIONS` | no | `10` | Max tool-calling loop iterations |
| `LOG_LEVEL` | no | `info` | `info` or `debug` |

## Deployment (DigitalOcean App Platform)

- Component type: **Worker** (polling, no inbound HTTP)
- Set all required env vars as secrets in the DO console
- Push to GitHub → auto-deploy triggers
- The GitHub workflow runs migrations in a dedicated job before deploying the worker. Set `DO_DATABASE_ID` and `DO_APP_ID` as GitHub Actions variables, and set `ACCESS_PIN` as a GitHub Actions secret.
