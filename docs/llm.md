---
title: Conversation-Aware LLM Replies and Tool Calls
description: OpenAI Responses API integration with persisted conversation turns and a bounded native tool-calling loop.
methods:
  - llm.Client.Generate: Sends conversation items and tool definitions and returns text, function calls, and usage metadata.
  - telegram.Handler.generateResponseWithTools: Executes and persists the bounded LLM/tool loop.
depends_on:
  - internal/llm/client.go
  - internal/config/config.go
  - internal/tools/registry.go
used_by:
  - cmd/bot/main.go
  - internal/telegram/handler.go
---

# Conversation-Aware LLM Replies

The bot starts one OpenAI Responses API flow for every authorized text message. At startup it loads `prompts/system_query.md` as the system instruction, then passes it with the newest persisted turns from the current Telegram conversation. The current user message is saved before that query, so it is included in the history sent to the model.

The request uses `store: false` and does not pass a previous response identifier. Conversation state stays in the application's PostgreSQL database and is supplied as structured `user`, `assistant`, encrypted `reasoning`, `function_call`, and `function_call_output` input items on every request. Requests include `reasoning.encrypted_content` so reasoning-model state can be continued without storing responses at OpenAI. This avoids mixing one Telegram topic's context with another's.

When the model requests a function, the handler persists the assistant tool call, executes it through the provider-independent registry, persists its result, and sends the expanded conversation back to the model. This repeats until the model returns final text or reaches `TOOL_CALL_MAX_ITERATIONS`. Tool execution errors are returned to the model as JSON so it can recover or explain the failure.

Before sending a model response, the bot converts common Markdown to Telegram HTML: headings become bold, and bold, italic, code, links, lists, quote markers, and horizontal rules are rendered safely. Other text is HTML-escaped.

## Configuration

`OPENAI_MODEL` selects the model and defaults to `gpt-5.5` when unset. `OPENAI_BASE_URL`, `OPENAI_API_KEY`, and `LLM_MAX_TOKENS` configure the endpoint, authentication, and response limit. `LLM_HISTORY_MAX_MESSAGES` defaults to 20 and bounds the number of persisted items sent as context. Raise this variable if a conversation needs a longer working history. The implementation uses the official `openai-go` SDK and sends only common Responses API fields, so the model can be safely overridden without model-specific reasoning settings.

Every provider attempt is recorded in `llm_requests`, including failures. The SDK returns input, cached-input, output, reasoning, and total tokens. At startup the bot loads the rate for the configured provider and model from the newest matching CSV snapshot in `tariffs/`. The filename becomes `pricing_version`, so later rate changes cannot alter cost history. See `tariffs/README.md` for the update procedure.
