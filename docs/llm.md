---
title: Conversation-Aware LLM Replies
description: OpenAI Responses API integration that provides persisted conversation turns as message context.
methods:
  - llm.Client.Generate: Sends structured user and assistant conversation messages and returns its text output.
depends_on:
  - internal/llm/client.go
  - internal/config/config.go
used_by:
  - cmd/bot/main.go
  - internal/telegram/handler.go
---

# Conversation-Aware LLM Replies

Slice 4 makes one OpenAI Responses API call for every authorized text message. It passes a fixed instruction and the newest persisted turns from the current Telegram conversation, then sends the resulting text to Telegram.

The request uses `store: false` and does not pass a previous response identifier. Conversation state stays in the application's PostgreSQL database and is supplied as structured `user` and `assistant` input items on every request. This avoids mixing one Telegram topic's context with another's.

## Configuration

`OPENAI_MODEL` selects the model and defaults to `gpt-5.5` when unset. `OPENAI_BASE_URL`, `OPENAI_API_KEY`, and `LLM_MAX_TOKENS` configure the endpoint, authentication, and response limit. `LLM_HISTORY_MAX_MESSAGES` defaults to 20 and bounds the number of persisted messages sent as context. The implementation uses the official `openai-go` SDK and sends only common Responses API fields, so the model can be safely overridden without model-specific reasoning settings.
