---
title: Tool Architecture
description: Provider-independent native and MCP tools, configuration, persistence, startup discovery, and execution loop.
methods:
  - tools.Registry.Register: Registers a uniquely named native or MCP-backed tool.
  - tools.Registry.RegisterAll: Atomically registers all tools discovered from one MCP server.
  - tools.Registry.Definitions: Returns deterministic LLM-facing tool definitions.
  - tools.Registry.Source: Returns internal provider metadata for user-facing progress.
  - tools.Registry.Execute: Dispatches JSON arguments to a tool by name.
  - tools.NewExecutionContext: Carries trusted Telegram conversation data to native tools.
  - currenttime.Tool.Execute: Returns the current time for an optional IANA timezone.
  - draft.Tool.Execute: Creates a collaborative draft from model content and trusted execution context.
  - draft.UpdateTool.Execute: Updates the active draft through the shared optimistic concurrency transaction.
  - mcpclient.Connect: Connects to one Streamable HTTP MCP server and discovers all advertised tools.
  - mcpclient.Connection.Close: Closes an MCP client session.
  - telegram.Handler.generateResponseWithTools: Runs and persists the bounded LLM/tool loop.
depends_on:
  - internal/tools/types.go
  - internal/tools/registry.go
  - internal/tools/currenttime/current_time.go
  - internal/tools/draft/create_draft.go
  - internal/tools/draft/update_draft.go
  - internal/tools/mcpclient/client.go
  - internal/config/config.go
  - internal/telegram/handler.go
  - migrations/00004_add_tool_messages.sql
used_by:
  - cmd/bot/main.go
  - internal/llm/client.go
---

# Tool Architecture

All tools implement one provider-independent interface containing an LLM-facing definition and an execution method that accepts JSON arguments. Definitions also carry an internal source label such as `native`, `wikipedia`, or `openstreetmap`; this label is used for user-facing progress but is not sent as part of the LLM function schema. The registry rejects duplicate names and returns definitions in deterministic name order. Native tools and MCP adapters share this registry. Batch registration is atomic, so a name collision cannot leave half of one server's tools active.

For tools that require identity, the handler adds a typed `ExecutionContext` to
the standard Go context just before execution. It contains the trusted Telegram
chat, topic, and user IDs from the incoming update. It is not part of the tool
schema or model-provided JSON, preventing a model call from selecting another
conversation or owner.

## Native draft creation

`create_draft(kind, body)` is always registered once PostgreSQL is available.
It accepts only the communication kind (`email`, `whatsapp`, or `generic`) and
the complete draft body. A successful call creates revision 1 through the
existing transactional repository, marks any previous active draft in that
conversation as superseded, and returns canonical JSON with its UUID, kind,
empty subject, body, and revision. The agent may use basic Markdown for lists,
bold, italic, and HTTPS links; it is converted into canonical Tiptap content
before persistence. The handler reconstructs that formatting for the complete
Telegram preview, separated by a divider, and shows the corresponding **Edit**
button after the model's final confirmation.

`update_draft(body, expected_revision)` is also registered after
PostgreSQL becomes available. Before each model generation, the handler loads
the authorized active draft and presents a transient JSON wrapper containing its
canonical Markdown body and revision. It is not persisted in `messages`.
`update_draft` receives the active draft ID and owner only from the trusted
execution context, replaces the complete canonical content, and reuses the
database's optimistic revision check. An editor save that wins the race causes
the tool to return a conflict rather than overwrite it.

## Native current-time tool

`current_time` accepts an optional `timezone` argument containing an IANA timezone. When omitted, it uses `TOOLS_CURRENT_TIME_DEFAULT_TIMEZONE`, which defaults to `Europe/Madrid`. Its JSON result contains the timezone, RFC 3339 local time, UTC offset, and Unix timestamp.

The tool is registered when `TOOLS_CURRENT_TIME_ENABLED=true`, the default. Invalid configured or requested timezones fail explicitly rather than falling back silently.

## Calling and persistence

The OpenAI client sends registered definitions as Responses API function tools. When the model returns one or more function calls, the Telegram handler persists encrypted reasoning continuation items, every assistant call, and then every paired tool result using the provider call ID. It sends those items back to the model and repeats until final text is returned or `TOOL_CALL_MAX_ITERATIONS` is reached.

Execution errors become JSON tool results with an `error` property. This gives the model an opportunity to recover without terminating the application process.

Every execution emits human-readable log messages such as `using tool current_time` and `tool current_time use completed in 1.2ms`. The same entries contain structured conversation identifiers, loop iteration, tool name, provider call ID, outcome, and duration in milliseconds. Successful calls also record the result length. Arguments and complete results are deliberately excluded from logs to avoid leaking sensitive data.

The user sees the same lifecycle through one editable Telegram status message. It starts as `💭 Pensant…`, changes to a human description of the active tool, and then to `✍️ Preparant la resposta…` before the next model call. These descriptions identify Wikipedia, OpenStreetMap, or the native time tool and the general operation, but deliberately omit raw tool arguments.

When `create_draft` successfully creates a draft, the status message instead
becomes the final confirmation followed by the complete canonical draft preview
after a horizontal divider. The preview reconstructs the stored basic
formatting—lists, bold, italic, and HTTPS links—and carries the precise Mini App
URL for that draft. Its delivered Telegram message ID is retained for later
refreshes.

## MCP servers

At startup, the bot connects independently to every enabled MCP server over Streamable HTTP, follows tool-list pagination, converts the advertised JSON schemas to common tool definitions, and registers the complete server batch. Tool names are preserved exactly as advertised; no server prefix is added by the bot.

MCP input schemas remain provider-independent in the registry. When the OpenAI client builds a request, it clones and normalizes each schema for OpenAI function definitions. The root is required to be an object. Top-level `oneOf`, `anyOf`, and `allOf` compositions are flattened into that object: branch properties are retained, requirements shared by every alternative remain required, and `allOf` requirements are combined. OpenAI-incompatible top-level `enum`, `const`, and `not` constraints are removed from the copy. The original arguments are still sent unchanged to the MCP server, which remains responsible for exact validation. This allows tools such as OpenStreetMap's `query_bbox` to work with models that reject composition keywords at the top of a function schema.

`TOOLS_MCPS` is a comma-separated list such as `wikipedia,openstreetmap`. Membership enables a server by default. `TOOLS_<NAME>_ENABLED` is an explicit override: `false` disables a listed server, while `true` can enable the known `wikipedia` or `openstreetmap` server when absent from the list. Enabled servers require `TOOLS_<NAME>_URL`. `TOOLS_<NAME>_TIMEOUT` defaults to `30s` and covers startup discovery and each call.

Authentication defaults to `TOOLS_<NAME>_AUTH_TYPE=none`. Set it to `bearer` and provide `TOOLS_<NAME>_TOKEN` for an internal endpoint that requires a token. Tokens and tool arguments are never written to logs.

If connection, initialization, schema conversion, or atomic registration fails, only that MCP server is closed and disabled until the next process restart. Other MCP servers and native tools continue loading. Runtime call errors are returned to the LLM as tool errors so it can recover. Dynamic tool-list notifications and hot reload are intentionally not supported in this first version.

For the local containers currently used by the project:

```dotenv
TOOLS_MCPS=wikipedia,openstreetmap
TOOLS_WIKIPEDIA_URL=http://127.0.0.1:8088/mcp
TOOLS_WIKIPEDIA_AUTH_TYPE=none
TOOLS_OPENSTREETMAP_URL=http://127.0.0.1:3010/mcp
TOOLS_OPENSTREETMAP_AUTH_TYPE=none
```

Those loopback URLs apply when the bot runs on the host. A bot container on Docker Desktop must use `host.docker.internal`, or all three containers must share a Docker network and use service names.

The OpenStreetMap MCP container separately needs an identifying `OSM_USER_AGENT` for the public Nominatim service. This setting belongs to that container, not to tourplannerbot. Replace placeholder contact data with a real project contact; Nominatim may reject non-compliant requests with HTTP 403.

Startup logs contain readable messages such as `loading MCP wikipedia` and `MCP wikipedia loaded 22 tools in 42ms`, together with structured `mcp_name`, URL, tool count, duration, and status fields. A failed server produces a readable disabled message plus its error. Normal MCP calls use the same per-tool start/completion/duration logs as native tools.
