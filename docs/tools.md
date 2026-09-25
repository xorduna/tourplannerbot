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
  - bigin.GetDealTool.Execute: Retrieves one Bigin pipeline record by its numeric record ID.
  - bigin.SearchContactsTool.Execute: Retrieves Bigin contacts by ID, general text, email, or phone.
  - bigin.AddDealNoteTool.Execute: Adds a note to one Bigin pipeline record.
  - brave.WebSearchTool.Execute: Searches the public web through Brave Search and returns compacted results.
  - gmail.CreateDraftTool.Execute: Creates an unsent plain-text Gmail draft.
  - gmail.UpdateDraftTool.Execute: Replaces the complete message in an existing Gmail draft.
  - draft.Tool.Execute: Creates a collaborative draft from model content and trusted execution context.
  - draft.UpdateTool.Execute: Updates the active draft through the shared optimistic concurrency transaction.
  - mcpclient.Connect: Connects to one Streamable HTTP MCP server and discovers all advertised tools.
  - mcpclient.Connection.Close: Closes an MCP client session.
  - telegram.Handler.generateResponseWithTools: Runs and persists the bounded LLM/tool loop.
depends_on:
  - internal/tools/types.go
  - internal/tools/registry.go
  - internal/tools/currenttime/current_time.go
  - internal/tools/bigin/client.go
  - internal/tools/bigin/get_deal.go
  - internal/tools/bigin/search_contacts.go
  - internal/tools/bigin/add_deal_note.go
  - internal/tools/brave/client.go
  - internal/tools/brave/web_search.go
  - internal/tools/gmail/client.go
  - internal/tools/gmail/create_draft.go
  - internal/tools/gmail/update_draft.go
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

## Native Bigin tools

`get_bigin_deal(deal_id)` retrieves one deal through the documented Bigin v2
pipeline-record endpoint, `GET /bigin/v2/Pipelines/{record_id}`. The ID remains
a string so large Zoho identifiers are never rounded. The response is returned
as the complete JSON envelope, including custom fields, for the model to inspect
and discuss with the user.

`search_bigin_contacts(search_by, query)` retrieves contacts with the complete
standard and custom field envelope returned by Bigin. `search_by=id` performs a
direct `GET /bigin/v2/Contacts/{record_id}` lookup and is preferred whenever a
contact ID is available. The `word`, `email`, and `phone` modes call the
documented Contacts search endpoint. A normal HTTP 204 no-results response is
normalized to `{"data":[]}` for the model.

`add_bigin_deal_note(deal_id, title, content)` creates one note through
`POST /bigin/v2/Pipelines/{record_id}/Notes`. The title is optional at the Bigin
API level; callers pass an empty string when it is not needed. The tool is
explicitly limited to user-requested writes, validates the numeric deal ID and
note content locally, and returns Bigin's complete operation response. The
refresh token needs pipeline creation access plus note creation access; the
minimal scopes documented by Bigin are `ZohoBigin.modules.pipelines.CREATE` and
`ZohoBigin.modules.notes.CREATE`.

The tool is enabled automatically when `TOOLS_BIGIN_REFRESH_TOKEN`,
`TOOLS_BIGIN_CLIENT_ID`, and `TOOLS_BIGIN_CLIENT_SECRET` are all present. A
partial credential set is a startup error; an entirely absent set leaves Bigin
disabled. Access tokens are refreshed through the EU Zoho Accounts endpoint,
cached until shortly before expiry, and refreshed once more after an HTTP 401.
Neither OAuth credentials nor record payloads are written to application logs.

The default endpoints are `https://accounts.zoho.eu` and
`https://www.zohoapis.eu`; they can be overridden with
`TOOLS_BIGIN_ACCOUNTS_URL` and `TOOLS_BIGIN_API_URL`. `TOOLS_BIGIN_TIMEOUT`
defaults to `30s`.

## Native Brave web search tool

`web_search(query, count, freshness)` searches the public web through the Brave
Search API endpoint `GET /res/v1/web/search`. It is intended for information
that changes over time, such as opening hours, prices, ticket availability,
events, transport, or news, where answering from model memory is unreliable.

`query` is required and is validated locally against the documented Brave
limits of 400 characters and 50 words. `count` is optional, defaults to
`TOOLS_BRAVE_COUNT`, and must be between 1 and 20. `freshness` is optional and
accepts only `pd`, `pw`, `pm`, or `py` for the last day, week, month, or year.

The tool deliberately does **not** forward Brave's complete response envelope.
It returns `{"query", "count", "results"}` where each result contains only the
title, URL, description, and, when available, the age of the page. Highlight
markup and HTML entities are removed, whitespace is collapsed, and descriptions
longer than 600 bytes are truncated on a UTF-8 boundary. This keeps a single
search from consuming an unreasonable part of the context window.

The tool is enabled as soon as `TOOLS_BRAVE_TOKEN` is configured; an absent
token leaves it disabled without failing startup. The token is sent in the
`X-Subscription-Token` header, is never written to logs, and is excluded from
the error messages returned to the model. Brave errors keep their code and
detail so the model can distinguish a rate limit from a bad request.

`TOOLS_BRAVE_API_URL` defaults to `https://api.search.brave.com` and exists for
tests and compatible gateways. `TOOLS_BRAVE_TIMEOUT` defaults to `30s`.

## Native Gmail tools

`create_gmail_draft(to, cc, bcc, subject, body)` creates an unsent plain-text
draft in the OAuth user's Gmail mailbox. It is exposed under this explicit name
because `create_draft` already belongs to the application's collaborative draft
editor. `to`, `cc`, and `bcc` are arrays of RFC mailbox strings; strict calls
must provide `cc` and `bcc` as empty arrays when unused. The tool validates and
canonicalizes every recipient, builds a CRLF-normalized RFC 2822 MIME message,
base64url-encodes it in `message.raw`, and calls
`POST /gmail/v1/users/me/drafts`. It returns only the draft, message, and
optional thread IDs. It never sends mail.

`update_gmail_draft(draft_id, to, cc, bcc, subject, body)` replaces the entire
message inside an existing draft through
`PUT /gmail/v1/users/me/drafts/{draft_id}`. Gmail keeps the draft resource ID
stable but replaces its underlying message, so the returned message ID may
change. The caller must therefore provide the complete recipient lists,
subject, and body rather than only the changed fields. The tool verifies that
the returned draft ID matches the requested resource and never sends mail.

The integration is enabled automatically when `TOOLS_GMAIL_REFRESH_TOKEN`,
`TOOLS_GMAIL_CLIENT_ID`, and `TOOLS_GMAIL_CLIENT_SECRET` are all present. A
partial set is a startup error and a completely absent set disables Gmail. The
refresh token must have been authorized with the
`https://www.googleapis.com/auth/gmail.compose` scope. Access tokens are
refreshed at `https://oauth2.googleapis.com/token`, cached until shortly before
expiry, and refreshed once more after an HTTP 401.

`TOOLS_GMAIL_OAUTH_URL` and `TOOLS_GMAIL_API_URL` override the default Google
endpoints for tests or compatible gateways. `TOOLS_GMAIL_TIMEOUT` defaults to
`30s`. Credentials and complete MIME content are excluded from logs.

## Calling and persistence

The OpenAI client sends registered definitions as Responses API function tools. When the model returns one or more function calls, the Telegram handler persists encrypted reasoning continuation items, every assistant call, and then every paired tool result using the provider call ID. It sends those items back to the model and repeats until final text is returned or `TOOL_CALL_MAX_ITERATIONS` is reached.

Execution errors become JSON tool results with an `error` property. This gives the model an opportunity to recover without terminating the application process.

Startup emits an INFO-level lifecycle for every tool. Native tools log their
`initialize`, `register`, and `ready` phases with source and duration. Disabled
native integrations and MCP servers log a non-sensitive reason. MCP discovery
logs each enabled server, the exact tool names it advertised, registration
failures, and a discovery summary. Once PostgreSQL-backed tools are registered,
the final inventory contains the deterministic list of available tool names,
the count, a source-to-tools mapping, and the number of live MCP connections.
Credentials are never included in these entries.

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
