---
title: Tool Architecture
description: Provider-independent tool registry, native current-time tool, persistence, configuration, and execution loop.
methods:
  - tools.Registry.Register: Registers a uniquely named native or MCP-backed tool.
  - tools.Registry.Definitions: Returns deterministic LLM-facing tool definitions.
  - tools.Registry.Execute: Dispatches JSON arguments to a tool by name.
  - currenttime.Tool.Execute: Returns the current time for an optional IANA timezone.
  - telegram.Handler.generateResponseWithTools: Runs and persists the bounded LLM/tool loop.
depends_on:
  - internal/tools/types.go
  - internal/tools/registry.go
  - internal/tools/currenttime/current_time.go
  - internal/telegram/handler.go
  - migrations/00004_add_tool_messages.sql
used_by:
  - cmd/bot/main.go
  - internal/llm/client.go
---

# Tool Architecture

All tools implement one provider-independent interface containing an LLM-facing definition and an execution method that accepts JSON arguments. The registry rejects duplicate names and returns definitions in deterministic name order. Native tools and future MCP adapters share this registry.

## Native current-time tool

`current_time` accepts an optional `timezone` argument containing an IANA timezone. When omitted, it uses `TOOLS_CURRENT_TIME_DEFAULT_TIMEZONE`, which defaults to `Europe/Madrid`. Its JSON result contains the timezone, RFC 3339 local time, UTC offset, and Unix timestamp.

The tool is registered when `TOOLS_CURRENT_TIME_ENABLED=true`, the default. Invalid configured or requested timezones fail explicitly rather than falling back silently.

## Calling and persistence

The OpenAI client sends registered definitions as Responses API function tools. When the model returns one or more function calls, the Telegram handler persists encrypted reasoning continuation items, every assistant call, and then every paired tool result using the provider call ID. It sends those items back to the model and repeats until final text is returned or `TOOL_CALL_MAX_ITERATIONS` is reached.

Execution errors become JSON tool results with an `error` property. This gives the model an opportunity to recover without terminating the application process.

Every execution emits human-readable messages such as `using tool current_time` and `tool current_time use completed in 1.2ms`. The same entries contain structured conversation identifiers, loop iteration, tool name, provider call ID, outcome, and duration in milliseconds. Successful calls also record the result length. Arguments and complete results are deliberately excluded from logs to avoid leaking sensitive data.

## Planned MCP support

The next iteration will add local MCP servers over Streamable HTTP. Servers will be selected through `TOOLS_MCPS`, with optional per-server `TOOLS_<NAME>_ENABLED` overrides and `none` or `bearer` authentication. A server that cannot initialize will be logged and disabled without preventing the rest of the application from starting.
