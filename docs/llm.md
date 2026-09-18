---
title: Conversation-Aware LLM Replies
description: OpenAI Responses API integration that provides persisted conversation turns as message context.
methods:
  - llm.Client.Generate: Sends structured user and assistant conversation messages and returns text plus usage metadata.
depends_on:
  - internal/llm/client.go
  - internal/config/config.go
used_by:
  - cmd/bot/main.go
  - internal/telegram/handler.go
---

# Conversation-Aware LLM Replies

The bot makes one OpenAI Responses API call for every authorized text message. At startup it loads `prompts/system_query.md` as the system instruction, then passes it with the newest persisted turns from the current Telegram conversation. The current user message is saved before that query, so it is included in the history sent to the model.

The request uses `store: false` and does not pass a previous response identifier. Conversation state stays in the application's PostgreSQL database and is supplied as structured `user` and `assistant` input items on every request. This avoids mixing one Telegram topic's context with another's.

## Configuration

`OPENAI_MODEL` selects the model and defaults to `gpt-5.5` when unset. `OPENAI_BASE_URL`, `OPENAI_API_KEY`, and `LLM_MAX_TOKENS` configure the endpoint, authentication, and response limit. `LLM_HISTORY_MAX_MESSAGES` defaults to 20 and bounds the number of persisted messages sent as context. Raise this variable if a conversation needs a longer working history. The implementation uses the official `openai-go` SDK and sends only common Responses API fields, so the model can be safely overridden without model-specific reasoning settings.

Every provider attempt is recorded in `llm_requests`, including failures. The SDK returns input, cached-input, output, reasoning, and total tokens. At startup the bot loads the rate for the configured provider and model from the newest matching CSV snapshot in `tariffs/`. The filename becomes `pricing_version`, so later rate changes cannot alter cost history. See `tariffs/README.md` for the update procedure.
