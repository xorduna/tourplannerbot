---
title: Local PostgreSQL Setup
description: Instructions for creating a local PostgreSQL user and database for Tour Planner Bot development.
methods: []
depends_on:
  - .env.example
used_by:
  - docker-compose.yml
  - README.md
---

# Local PostgreSQL Setup

To create a new user and database for the Tour Planner Bot, use the following SQL commands. Replace `your_strong_password` with a secure password of your choice.

```sql
CREATE USER tourplannerbot WITH PASSWORD 'your_strong_password';
CREATE DATABASE tourplannerbot OWNER tourplannerbot;
GRANT ALL PRIVILEGES ON DATABASE tourplannerbot TO tourplannerbot;
```

Env var `DATABASE_URL` should then be set to:

```
postgres://tourplannerbot:your_strong_password@localhost:5432/tourplannerbot
```
