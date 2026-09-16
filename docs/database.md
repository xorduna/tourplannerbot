---
title: Database and Migration Operations
description: PostgreSQL connection, GORM persistence, Goose migration, and DigitalOcean deployment procedures.
methods:
  - database.Open: Opens and validates the GORM PostgreSQL connection.
  - telegram.Handler.authorizeUser: Queries and creates authorized users through GORM.
depends_on:
  - migrations/00001_create_allowed_users.sql
  - internal/database/database.go
  - internal/models/allowed_user.go
  - .github/workflows/deploy.yml
used_by:
  - cmd/bot/main.go
  - Makefile
---

# Database and Migration Operations

## Application Database Access

The bot uses GORM with PostgreSQL. `DATABASE_URL` is required at startup; the process checks the connection before creating the Telegram bot. Models are stored in `internal/models/`; the Telegram handler uses direct GORM queries for the current simple authorization flow. The schema is managed only by Goose migrations, not by GORM auto-migration.

## Local Development

Set `DATABASE_URL` in `.env`, then run:

```bash
make db-up
make goose-install
make migrate-up
```

Migrations live in `migrations/` as sequential Goose SQL files. Check their status with `make migrate-status`.

## Production Deployment

The `migrate` GitHub Actions job runs after the image build and before the worker deployment. It:

1. Looks up the GitHub runner's public IP address.
2. Temporarily adds that IP as a DigitalOcean Managed Database trusted source.
3. Fetches the database connection URL with `doctl`, then runs the Goose binary.
4. Removes the temporary trusted-source rule even if migration fails.

The `deploy` job additionally grants the App Platform application persistent database access and injects `DATABASE_URL` as a runtime secret.

Before enabling this flow, configure these GitHub Actions values:

- Repository variable: `DO_APP_ID`. `DO_DATABASE_ID` may be set as a repository variable to override the database ID configured in the workflow.
- Repository secrets: `DIGITALOCEAN_ACCESS_TOKEN`, `TELEGRAM_BOT_TOKEN`, `OPENAI_API_KEY`, and `ACCESS_PIN`.
