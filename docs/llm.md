---
title: Stateless LLM Replies
description: OpenAI Responses API integration for the Slice 3 one-message joke response.
methods:
  - llm.Client.Generate: Sends one stateless Responses API request and returns its text output.
depends_on:
  - internal/llm/client.go
  - internal/config/config.go
used_by:
  - cmd/bot/main.go
  - internal/telegram/handler.go
---

# Stateless LLM Replies

Slice 3 makes one OpenAI Responses API call for every authorized text message. It passes a fixed instruction to return a concise, clean joke related to the message, then sends the resulting text to Telegram.

The request uses `store: false` and does not pass a previous response identifier or message history. Consequently, every response is independent; persistent chat history begins in Slice 4.

## Configuration

`OPENAI_MODEL` selects the model and defaults to `gpt-5.5` when unset. `OPENAI_BASE_URL`, `OPENAI_API_KEY`, and `LLM_MAX_TOKENS` configure the endpoint, authentication, and response limit. The implementation uses the official `openai-go` SDK and sends only common Responses API fields, so the model can be safely overridden without model-specific reasoning settings.
