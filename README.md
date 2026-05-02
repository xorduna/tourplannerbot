# tourplannerbot

Telegram bot for Diana, a licensed Barcelona tour guide. Internal tool to plan trips and answer quick queries using OpenAI.

## Features (current: Slice 1 — Echo Bot)

- Echoes messages back (foundation for all future slices)
- Config loaded from environment variables
- Structured JSON logging via `slog`
- Graceful shutdown on SIGTERM

## Project Structure

```
cmd/bot/main.go               # Entrypoint
internal/
  config/config.go            # Env var loading
  telegram/handler.go         # Message routing
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

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `TELEGRAM_BOT_TOKEN` | yes | — | Bot token from @BotFather |
| `OPENAI_API_KEY` | yes | — | OpenAI (or compatible) API key |
| `DATABASE_URL` | yes | — | PostgreSQL connection string |
| `ACCESS_PIN` | yes | — | PIN users must enter to unlock the bot |
| `OPENAI_MODEL` | no | `gpt-4o-mini` | Model name |
| `OPENAI_BASE_URL` | no | `https://api.openai.com/v1` | Base URL (swap for OpenRouter etc.) |
| `LLM_MAX_TOKENS` | no | `2048` | Max tokens per LLM response |
| `TOOL_CALL_MAX_ITERATIONS` | no | `10` | Max tool-calling loop iterations |
| `LOG_LEVEL` | no | `info` | `info` or `debug` |

## Deployment (DigitalOcean App Platform)

- Component type: **Worker** (polling, no inbound HTTP)
- Set all required env vars as secrets in the DO console
- Push to GitHub → auto-deploy triggers
