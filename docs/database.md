---
title: Database and Migration Operations
description: PostgreSQL connection, GORM persistence, Goose migration, and DigitalOcean deployment procedures.
methods:
  - buildinfo.Current: Returns the version and UTC build time embedded in the binary.
  - database.Open: Opens and validates the GORM PostgreSQL connection.
  - telegram.Handler.authorizeUser: Queries and creates authorized users through GORM.
  - telegram.Handler.loadConversationMessages: Retrieves recent history for one chat and topic.
depends_on:
  - internal/buildinfo/buildinfo.go
  - migrations/00001_create_allowed_users.sql
  - migrations/00002_create_messages.sql
  - migrations/00004_add_tool_messages.sql
  - internal/database/database.go
  - internal/database/readiness.go
  - internal/models/allowed_user.go
  - internal/models/message.go
  - .github/workflows/deploy.yml
used_by:
  - cmd/bot/main.go
  - Makefile
---

# Database and Migration Operations

## Application Database Access

The bot uses GORM with PostgreSQL. `DATABASE_URL` is required at startup; the Telegram bot waits and retries until PostgreSQL is available, while the HTTP liveness endpoint remains available and readiness returns `503`. Models are stored in `internal/models/`; the Telegram handler uses direct GORM queries for authorization and conversation history. The schema is managed only by Goose migrations, not by GORM auto-migration.

The `messages` table isolates history by `(chat_id, message_thread_id)`. Telegram uses a zero `message_thread_id` for private chats and groups without forum topics, while each forum topic has its own non-zero thread ID. This lets a topic represent one tour deal without leaking context from other topics in the same group. Migration `00004` adds `tool` and `reasoning` roles plus the call ID, tool name, and JSON arguments needed to reconstruct stateless Responses API function calls, encrypted reasoning state, and outputs.

The `llm_requests` table records each LLM attempt, including failures. It stores provider/model, duration, token usage, an optional immutable cost estimate, and a link to the source user message; it intentionally does not duplicate prompt or response text. Use its timestamp, model, and conversation indexes for consumption reports.

## Local Development

Set `DATABASE_URL` in `.env`, then run:

```bash
make db-up
make goose-install
make migrate-up
```

Migrations live in `migrations/` as sequential Goose SQL files. Check their status with `make migrate-status`.

## Production Deployment

The `migrate` GitHub Actions job runs after the image build and before the service deployment. It:

1. Opens the database firewall for the GitHub runner's public IP address.
2. Installs the pinned Goose release binary without installing Go, then uses the `DATABASE_URL` GitHub Actions secret to run it.
3. Removes the temporary firewall rule, including when the migration step fails.

These are separate workflow steps on the same runner, so the migration error and firewall cleanup are visible independently in GitHub Actions. The workflow delegates the shell logic to the versioned scripts in `scripts/`.

The build job creates an immutable `<branch>_<short-sha>` image tag, embeds that version and the UTC build time in the Go binary, and pushes both the immutable tag and `latest`. The deployment always references the immutable tag. The service exposes the embedded metadata as JSON from `/healthz`, while `/readyz` checks the database without causing a restart while it is temporarily unavailable.

The `deploy` job injects the `DATABASE_URL` GitHub Actions secret into the app-level runtime environment. It first verifies that the DigitalOcean token can read the configured app and uses `doctl apps propose` to validate the rendered spec as a non-mutating update. It then uses `doctl apps update --wait`, so the GitHub Actions job waits for App Platform to finish the rollout and reports a failed deployment as a failed workflow. The App Platform application must be authorized as a database trusted source before deployment.

Before enabling this flow, configure these GitHub Actions values:

- Repository variables: `DO_APP_ID`; `DO_DATABASE_ID` may override the database ID configured in the workflow. The production `APP_BASE_URL` is declared in `.do/app.yaml`.
- Repository secrets: `DIGITALOCEAN_ACCESS_TOKEN`, `DATABASE_URL`, `TELEGRAM_BOT_TOKEN`, `OPENAI_API_KEY`, and `ACCESS_PIN`.
