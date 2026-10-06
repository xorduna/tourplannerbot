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
  - randomnumber.Tool.Execute: Generates secure integer or floating-point random values in a range.
  - bigin.GetDealTool.Execute: Retrieves one Bigin pipeline record by its numeric record ID.
  - bigin.GetDealEmailsTool.Execute: Retrieves the email related-list data for a deal.
  - bigin.DownloadDealAttachmentTool.Execute: Lists or temporarily downloads one Bigin deal attachment for analysis.
  - bigin.UploadDealAttachmentTool.Execute: Uploads a queued temporary file as an attachment on a Bigin deal.
  - bigin.SearchContactsTool.Execute: Retrieves Bigin contacts by ID, general text, email, or phone.
  - bigin.AddDealNoteTool.Execute: Adds a note to one Bigin pipeline record.
  - bigin.UpdateDealTool.Execute: Updates selected Bigin fields and deep-merges formatted JSON metadata paths.
  - filesystem.Tool.Execute: Renames a file queued for Telegram delivery in the active conversation.
  - monei.CreatePaymentLinkTool.Execute: Creates a MONEI payment link from neutral payment inputs.
  - monei.GetPaymentTool.Execute: Retrieves one MONEI payment by ID.
  - brave.WebSearchTool.Execute: Searches the public web through Brave Search and returns compacted results.
  - jina.ReadURLTool.Execute: Reads one web page through Jina Reader and returns its Markdown content.
  - knowledgebase.ListPagesTool.Execute: Lists Markdown paths in the Diana Barcelona knowledge base.
  - knowledgebase.ListToursTool.Execute: Lists compact structured tour metadata from the knowledge base.
  - knowledgebase.GetTourTool.Execute: Retrieves one tour and calculates its booking estimate for 1–9 people.
  - knowledgebase.ReadPageTool.Execute: Reads one listed knowledge-base Markdown page.
  - gmail.CreateDraftTool.Execute: Creates an unsent plain-text Gmail draft.
  - gmail.DownloadAttachmentsTool.Execute: Downloads Gmail attachments, queues them for Telegram delivery, and makes bounded supported files available to the active model response.
  - gmail.SearchMessagesTool.Execute: Searches Gmail messages by sender, recipient, subject, or message text.
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
  - internal/tools/randomnumber/random_number.go
  - internal/tools/bigin/client.go
  - internal/tools/bigin/get_deal.go
  - internal/tools/bigin/get_deal_emails.go
  - internal/tools/bigin/download_deal_attachment.go
  - internal/tools/bigin/search_contacts.go
  - internal/tools/bigin/add_deal_note.go
  - internal/tools/bigin/update_deal.go
  - internal/tools/bigin/upload_deal_attachment.go
  - internal/tools/filesystem/filesystem.go
  - internal/tools/monei/client.go
  - internal/tools/monei/payment.go
  - internal/tools/brave/client.go
  - internal/tools/brave/web_search.go
  - internal/tools/jina/client.go
  - internal/tools/jina/read_url.go
  - internal/tools/knowledgebase/client.go
  - internal/tools/knowledgebase/tools.go
  - internal/tools/gmail/client.go
  - internal/tools/gmail/create_draft.go
  - internal/tools/gmail/download_attachments.go
  - internal/tools/gmail/search_messages.go
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

## Native random-number tool

`random_number(mode, minimum, maximum)` generates one value using the operating
system's cryptographically secure random source. `mode=integer` accepts only
signed 64-bit integer limits and returns an unbiased value in the inclusive
range `[minimum, maximum]`. `mode=float` accepts finite numeric limits and
returns a value in the half-open range `[minimum, maximum)`. Equal float bounds
return that exact value. The tool has no configuration or external service
dependency and is always registered.

## Native Bigin tools

`get_bigin_deal(deal_id)` retrieves one deal through the documented Bigin v2
pipeline-record endpoint, `GET /bigin/v2/Pipelines/{record_id}`. The ID remains
a string so large Zoho identifiers are never rounded. The response is returned
as the complete JSON envelope, including custom fields, for the model to inspect
and discuss with the user.

`get_bigin_deal_emails(deal_id, message_id)` retrieves the Bigin email related
list at `GET /bigin/v2/Pipelines/{record_id}/Emails` when `message_id` is
`null`. Providing a message ID from that list retrieves the individual email,
including its `content` body, through the corresponding detail endpoint. It is
read-only and returns Bigin's unchanged response.

`download_bigin_deal_attachment(deal_id, attachment_id)` lists the attachment
related list through `GET /bigin/v2/Pipelines/{record_id}/Attachments` when
`attachment_id` is `null`. With an attachment ID from that list it verifies
that the attachment belongs to the deal, downloads it from
`GET /bigin/v2/Pipelines/{record_id}/Attachments/{attachment_id}`, and stores
it only in a private temporary directory. Supported PDFs, text files, rich
documents, presentations, and spreadsheets are provided to the next model
request as untrusted source material so they can be analysed; the file and
directory are deleted once the response ends. Downloads are not sent to
Telegram by default. `send_to_telegram` can be set to `true` only when the user
explicitly asks to receive the file. Each download is limited to 20 MB.

`upload_bigin_deal_attachment(deal_id, filename)` uploads one file already
queued earlier in the same response to `POST /bigin/v2/Pipelines/{record_id}/Attachments`
as `multipart/form-data`. The filename must exactly match a file returned by
`download_gmail_attachments` in that same response cycle; paths cannot be
provided by the model. The tool accepts regular files in the bot's dedicated
`./tmp` workspace only, rejects `.exe` files, and enforces Bigin's 20 MB
per-file limit.

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

`update_bigin_deal(deal_id, updates)` is the standard focused write primitive.
Each update carries a top-level Bigin field API name and a scalar value, so a
negotiated price can use `Amount` and a payment link can use `Payment_Link`.
Only listed fields are sent to Bigin; all unmentioned deal fields remain
unchanged. For JSON metadata, use paths such as
`metadata.monei_payment_id` or `metadata.tour.language`. The tool reads the
current metadata, deep-merges each listed path, and serializes the entire value
with two-space indentation before it writes the configured metadata field. This
prevents accidental replacement of unrelated metadata keys. The metadata field
API name defaults to `metadata`, automatically matches capitalization returned
by Bigin (for example `metadata`), and can be set exactly with
`TOOLS_BIGIN_METADATA_FIELD`. After every PUT, the tool reads the deal again
and verifies every requested path. It returns `verified_updates` only when all
values persisted; an ignored field becomes an explicit error naming the failed
paths, rather than a misleading successful update.

The tool is enabled automatically when `TOOLS_BIGIN_REFRESH_TOKEN`,
`TOOLS_BIGIN_CLIENT_ID`, and `TOOLS_BIGIN_CLIENT_SECRET` are all present. A
partial credential set is a startup error; an entirely absent set leaves Bigin
disabled. Bigin documents `ZohoBigin.modules.ALL` for related-list access, so
the email tool needs a refresh token authorized with that scope. Access tokens
are refreshed through the EU Zoho Accounts endpoint,
cached until shortly before expiry, and refreshed once more after an HTTP 401.
Neither OAuth credentials nor record payloads are written to application logs.

The default endpoints are `https://accounts.zoho.eu` and
`https://www.zohoapis.eu`; they can be overridden with
`TOOLS_BIGIN_ACCOUNTS_URL` and `TOOLS_BIGIN_API_URL`. `TOOLS_BIGIN_TIMEOUT`
defaults to `30s`.

## Native MONEI payment tools

`create_monei_payment_link(amount, customer_email, customer_name,
expiration_date, order_id, summary, allowed_payment_methods)` is intentionally
deal-agnostic. It accepts the business amount as an exact EUR decimal string
and converts it to MONEI's integer cents itself. It creates a `SALE` payment
through `POST /v1/payments` with `currency: EUR`, the selected `bizum` and/or
`card` payment methods, customer fields, an `expireAt` Unix timestamp, and a
MONEI `metadata.summary` field. An ISO date expires at 23:59:59 in
Europe/Madrid; an RFC 3339 timestamp is preserved exactly. The result includes
the API payment object plus `payment_link`, constructed from the configured
Diana Barcelona canonical base (`https://www.dianabarcelona.com/pay` by
default) and MONEI's returned ID.

`get_monei_payment(payment_id)` reads the complete current object through
`GET /v1/payments/{payment_id}`. It accepts only an opaque payment ID, never a
full URL, so callers must extract the ID from Bigin's `Payment Link` first.

MONEI is enabled only when `TOOLS_MONEI_API_KEY` is configured. Its default API
endpoint is `https://api.monei.com`, and `TOOLS_MONEI_API_URL`,
`TOOLS_MONEI_TIMEOUT` (default `30s`), and `TOOLS_MONEI_PAYMENT_LINK_BASE_URL`
are available for compatible endpoints, tests, and a future custom-domain
change. The API key is sent in MONEI's `Authorization` header and is never
logged.

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

## Native Jina page reader tool

`read_url(url, with_links)` retrieves one web page through the Jina AI Reader
API and returns it as Markdown. It complements `web_search`: the search tool
finds candidate pages, and this tool reads the chosen one. Jina renders
JavaScript before extracting text, so it also reads pages a plain HTTP fetch
returns empty.

The target address travels in a JSON body sent to `POST https://r.jina.ai/`
rather than appended to the Reader path. This keeps query strings and fragments
intact without a second layer of URL escaping. `url` is validated locally and
must be an absolute `http` or `https` address, so the tool cannot be steered
into another URL scheme.

`with_links` is optional. When true, the request sets `X-With-Links-Summary`
and the result carries the page's outgoing links so the model can navigate
further, for example from a venue's home page to its opening hours. The list is
sorted deterministically and capped at 50 entries so a link-heavy page cannot
dominate the result.

The tool returns `{"url", "title", "description", "published_time",
"http_status", "content", "truncated", "links", "warning"}`. Page Markdown
longer than `TOOLS_JINA_MAX_CONTENT_SIZE` is cut on a UTF-8 boundary and
reported through `truncated` rather than silently shortened. A reachable page
with no readable text returns empty content plus an explicit warning, so the
model can tell it apart from a failed read.

Two upstream behaviours are deliberately surfaced rather than hidden. Jina
answers HTTP 200 even when the requested page was itself an error page, so the
page's own `http_status` is forwarded for the model to judge. Fetch failures
such as an unresolvable domain or a navigation timeout arrive as HTTP 422 with
a `readableMessage`; the tool keeps that reason, discards the multi-line
navigation call log appended to timeouts, and never includes the API token.

The tool is enabled as soon as `TOOLS_JINA_TOKEN` is configured; an absent
token leaves it disabled without failing startup. `TOOLS_JINA_READER_URL`
defaults to `https://r.jina.ai` and exists for tests and compatible gateways.
`TOOLS_JINA_TIMEOUT` defaults to `60s` because rendering JavaScript is
noticeably slower than a plain fetch.

## Native Diana Barcelona knowledge-base tools

The knowledge base is a private S3-compatible DigitalOcean Spaces bucket, not
a vector store. It contains structured Hugo Markdown beneath
`TOOLS_KNOWLEDGEBASE_PREFIX`, which defaults to `web/content`. The application
uses four fixed native tools: `list_knowledge_base_pages`,
`list_knowledge_base_tours`, `get_knowledge_base_tour`, and
`read_knowledge_base_page`. Therefore the callable surface is stable even when
editors add or update Markdown files in the bucket.

The first call creates an inventory and parses tour frontmatter. It refreshes
after `TOOLS_KNOWLEDGEBASE_REFRESH_INTERVAL`, defaulting to `5m`; simultaneous
calls share one refresh. Markdown bodies are read only for the selected page
or tour. The bucket keys are never accepted directly: page reads must use a
relative Markdown path returned by the current inventory, preventing traversal
outside the configured prefix.

`get_knowledge_base_tour(identifier, people)` accepts a tour code or listed
relative path. `people` defaults to 2 and is restricted to 1–9. The returned
total is `price + (people × price_per_person)` when `price_per_person` exists;
otherwise it is only `price`. The base price is never multiplied by the group
size. Every result also includes the derived canonical Diana Barcelona URL and
the source Markdown. An explicit `url` frontmatter field overrides the usual
Hugo path mapping, and `slug` changes the final segment.

The integration enables only when all of `TOOLS_KNOWLEDGEBASE_ENDPOINT`,
`TOOLS_KNOWLEDGEBASE_ACCESS_KEY`, `TOOLS_KNOWLEDGEBASE_SECRET_KEY`, and
`TOOLS_KNOWLEDGEBASE_BUCKET_NAME` are present. `TOOLS_KNOWLEDGEBASE_REGION`
defaults to `lon1`; `TOOLS_KNOWLEDGEBASE_TIMEOUT` defaults to `30s`. Requests
use AWS Signature Version 4 with service `s3`. Credentials, document contents,
and tool arguments are excluded from logs. Retrieved Markdown is source data,
not executable model instructions.

## Native Gmail tools

`search_gmail_messages(from, to, subject, body, max_results, page_token)`
searches the OAuth user's mailbox using any non-empty combination of the four
dedicated filters. It returns at most 20 messages (10 by default), each with
its message and thread IDs plus `From`, `To`, `Subject`, `Date`, and Gmail's
compact snippet. The list endpoint returns only IDs, so the tool follows each
result with a metadata-only `messages.get` request; it does not retrieve full
body content or attachments itself. The optional `next_page_token` can be
passed back unchanged with the same filters for the next page. Every filter is
converted to a quoted literal, so it cannot introduce Gmail search operators.

`download_gmail_attachments(message_id)` retrieves all named attachment MIME
parts from the selected message and queues them for delivery to the same
Telegram chat and forum topic that requested the tool. It must only be used
when the user explicitly asks to retrieve or send an attachment. The tool
downloads at most five files, with a 45 MB per-file and 100 MB combined limit;
oversized or excess files are returned as skipped. Downloaded bytes are written
to a fresh private directory beneath the bot's `./tmp` workspace. Supported
PDFs, text files, rich documents, presentations, and spreadsheets that fit a
separate 45 MB combined model-input budget are also provided to the next
Responses request as temporary `input_file` data URLs so the active model can
read them; their contents are explicitly untrusted source material, not
instructions. No attachment is persisted in conversation history, tool results,
or OpenAI Files. Once Telegram has attempted each `sendDocument` upload, the
entire directory is removed, including when the response generation fails.

## Native filesystem tool

`filesystem(command, filename, new_filename)` currently supports only
`command=rename_file`. It physically renames a file that was downloaded in the
same tool loop, then updates the queued Telegram delivery to use the new name.
All managed files live under the bot's `./tmp` workspace; paths are never
provided to the model, and the tool rejects any file outside that workspace.
The workspace is created automatically at startup and its contents are ignored
by Git. New filesystem commands can extend the `command` enum without adding a
new tool name.

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
refresh token must have been authorized with both the
`https://www.googleapis.com/auth/gmail.compose` and
`https://www.googleapis.com/auth/gmail.readonly` scopes. Existing compose-only
refresh tokens must be reauthorized before the search tool can access the
mailbox. Access tokens are refreshed at `https://oauth2.googleapis.com/token`,
cached until shortly before expiry, and refreshed once more after an HTTP 401.

`TOOLS_GMAIL_OAUTH_URL` and `TOOLS_GMAIL_API_URL` override the default Google
endpoints for tests or compatible gateways. `TOOLS_GMAIL_TIMEOUT` defaults to
`30s`. Credentials, attachment bytes, complete MIME content, and temporary
filesystem paths are excluded from logs.

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

Telegram link previews are disabled on every message that can carry model text:
the final response, the fallback send path, both draft previews, and the
generated topic introduction. Answers cite the page each web fact came from, so
one message commonly holds several links and Telegram would otherwise expand
the first one into a large card that buries the answer. The option is a
`*bool`, so `disabledLinkPreview` is covered by a test asserting it serializes
as `{"is_disabled":true}`; a nil pointer would be omitted from the request and
previews would silently return.

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
