---
title: Conversation-Aware LLM Replies and Tool Calls
description: OpenAI Responses API integration, persisted tool loops, live Telegram progress, and safe rich response formatting.
methods:
  - llm.Client.Generate: Sends conversation items and tool definitions and returns text, function calls, and usage metadata.
  - llm.normalizeFunctionParameters: Clones and adapts function schemas to OpenAI's accepted top-level object shape.
  - telegram.Handler.generateResponseWithTools: Executes and persists the bounded LLM/tool loop.
  - telegram.newTelegramResponseProgress: Starts the editable thinking message and typing indicator.
  - telegram.telegramResponseProgress.finish: Replaces progress with the final normal or rich response.
  - telegram.formatTelegramRichHTML: Converts safe Markdown tables to native Telegram Rich HTML tables.
  - telegram.formatTelegramHTML: Converts Markdown to regular Telegram HTML with readable table fallbacks.
depends_on:
  - internal/llm/client.go
  - internal/llm/schema.go
  - internal/config/config.go
  - internal/telegram/progress.go
  - internal/tools/registry.go
used_by:
  - cmd/bot/main.go
  - internal/telegram/handler.go
---

# Conversation-Aware LLM Replies

The bot starts one OpenAI Responses API flow for every authorized text message. At startup it loads `prompts/system_query.md` as the system instruction, then passes it with the newest persisted turns from the current Telegram conversation. The current user message is saved before that query, so it is included in the history sent to the model.

The request uses `store: false` and does not pass a previous response identifier. Conversation state stays in the application's PostgreSQL database and is supplied as structured `user`, `assistant`, encrypted `reasoning`, `function_call`, and `function_call_output` input items on every request. Requests include `reasoning.encrypted_content` so reasoning-model state can be continued without storing responses at OpenAI. This avoids mixing one Telegram topic's context with another's.

When the model requests a function, the handler persists the assistant tool call, executes it through the provider-independent registry, persists its result, and sends the expanded conversation back to the model. This repeats until the model returns final text or reaches `TOOL_CALL_MAX_ITERATIONS`. Tool execution errors are returned to the model as JSON so it can recover or explain the failure.

Before an OpenAI request, the client clones every provider-independent tool schema and makes only the copy compatible with OpenAI function parameters. The root remains an object; top-level `oneOf`, `anyOf`, and `allOf` branches are flattened while preserving their properties and compatible requirements, and unsupported root constraints are removed. The registry retains the original schema for other providers, while the MCP server still performs exact argument validation at execution time.

For an authorized text request, the bot immediately posts `💭 Pensant…` and refreshes Telegram's typing action while work continues. A tool call edits that same message with a short Catalan activity description such as `🔎 Utilitzant Wikipedia per buscar informació…`; after the tool completes it becomes `✍️ Preparant la resposta…`. Tool arguments are never copied into these updates. The final answer replaces the same message, avoiding a trail of temporary status messages.

Progress delivery is best-effort and does not interrupt model or tool execution. If the initial placeholder cannot be sent, the final answer is sent normally. If the placeholder cannot be edited, the bot sends the final answer as a new message and then removes the stale placeholder only after successful delivery. The typing goroutine is always stopped before final delivery.

Before sending a model response, the bot converts common Markdown to Telegram-safe HTML: headings become bold, and bold, italic, code, links, lists, quote markers, and horizontal rules are rendered safely. Other text is HTML-escaped.

When a response contains a valid GitHub-style Markdown table, the bot edits the progress message into a Telegram Rich Message with a native bordered, striped, compact table. Cell contents are passed through the same safe inline formatter, so model-provided raw HTML cannot introduce Telegram elements. If a Rich Message send or edit fails, the bot retries as a regular HTML message in which every row becomes a labeled, mobile-friendly card instead of exposing Markdown pipe syntax.

## Configuration

`OPENAI_MODEL` selects the model and defaults to `gpt-5.5` when unset. `OPENAI_BASE_URL`, `OPENAI_API_KEY`, and `LLM_MAX_TOKENS` configure the endpoint, authentication, and response limit. `LLM_HISTORY_MAX_MESSAGES` defaults to 20 and bounds the number of persisted items sent as context. Raise this variable if a conversation needs a longer working history. The implementation uses the official `openai-go` SDK and sends only common Responses API fields, so the model can be safely overridden without model-specific reasoning settings.

Every provider attempt is recorded in `llm_requests`, including failures. The SDK returns input, cached-input, output, reasoning, and total tokens. At startup the bot loads the rate for the configured provider and model from the newest matching CSV snapshot in `tariffs/`. The filename becomes `pricing_version`, so later rate changes cannot alter cost history. See `tariffs/README.md` for the update procedure.
