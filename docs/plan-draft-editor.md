---
title: Collaborative Draft Editor Plan
description: Architecture and incremental implementation plan for an authenticated Telegram Mini App draft editor.
methods: []
depends_on:
  - README.md
  - .env.example
  - .do/app.yaml
  - internal/config/config.go
used_by:
  - cmd/bot/main.go
---

# Plan: collaborative draft editor

## Goal

Allow the assistant and the user to work on the same editable text, initially for emails and WhatsApp messages:

1. The user asks the bot to draft a text.
2. The agent creates a persistent `draft` and responds with a preview and an **Edit** button.
3. The button opens an authenticated Telegram Mini App.
4. The user modifies and saves the text.
5. In the next message, the agent always receives the most recent version of the active draft.
6. The draft remains active until the user explicitly asks to create a new one.

Selecting and recovering previous drafts is outside the first MVP. However, the data model must not prevent adding it later.

## Architecture decisions

### The draft is an entity, not a model response

The editable text will be a persistent domain entity, separate from `messages`. The model response will remain normal text. Agent tools will be the interface for creating and updating the draft.

There are three different representations:

- PostgreSQL contains the canonical version of the draft.
- Tools exchange structured JSON with the model.
- Telegram uses `InlineKeyboardMarkup` only to present the button that opens the Mini App.

Special tags generated inside the assistant's final text will not be parsed.

### Identity and active draft

- Drafts will have a public, non-sequential UUID.
- There will be at most one `active` draft per `(chat_id, message_thread_id)`.
- Creating a new draft will mark the previous one as `superseded` within the same transaction.
- `owner_telegram_id` will be used to enforce authorization and prepare for the case of multiple users.
- The user will only be able to edit drafts they can access; knowing a UUID will not grant access.

### Rich-text content

The frontend will use **SolidJS + TypeScript + Vite**. The editor will be **Tiptap Core**, integrated directly into a Solid component through its framework-agnostic API, without depending on a community Solid wrapper.

The initial editor configuration will be deliberately small:

- paragraphs and line breaks;
- bold and italic;
- bulleted and numbered lists;
- undo/redo;
- links only if they do not complicate the first delivery.

Headings, tables, images, colors, fonts, and real-time collaboration will not be included.

The canonical rich-content representation will be ProseMirror/Tiptap JSON (`content_json`). A plain-text projection (`body_text`) will also be stored so that:

- the draft can be passed to the model easily;
- Telegram can display a preview;
- it can later be copied or exported to WhatsApp without HTML.

The backend will validate a closed set of nodes and marks and derive `body_text` from `content_json`; it will not trust a text projection sent by the browser. When the agent creates or replaces a text, the backend will convert the plain text into a simple Tiptap document.

### Frontend served by Go

In production, Vite will generate versioned assets and Go will include them in the binary with `go:embed`. The HTML, assets, and API will be served from the same origin, avoiding CORS.

Suggested structure:

```text
web/
  package.json
  vite.config.ts
  src/
internal/webapp/
  server.go
  auth.go
  assets.go
  dist/                 # Vite output, embedded in the binary
```

The production build will be multi-stage:

1. a Node stage runs `npm ci` and `npm run build`;
2. a Go stage compiles after `internal/webapp/dist` exists;
3. the final image continues to contain a single binary.

The Make and CI targets must build the frontend before `go build` and before any test that compiles the package containing the embed.

### Process and deployment

The current DigitalOcean component will change from a `worker` to a `service`, because workers cannot receive HTTP traffic.

The same process will run concurrently:

- Telegram bot long polling;
- the HTTP server listening on `0.0.0.0:$PORT`.

`instance_count: 1` will be maintained: horizontally scaling the current process would result in multiple consumers polling the same bot. If the API needs to scale later, it can be split again into a `worker` for the bot and a `service` for the web application.

Planned new configuration:

```dotenv
APP_BASE_URL=https://example.ondigitalocean.app
PORT=8080
TELEGRAM_WEBAPP_AUTH_MAX_AGE=5m
```

`PORT` may have a local default value, but production will respect the value provided by the platform.

### Local development and HTTPS tunnel

Telegram needs an HTTPS URL accessible from the client, so testing the button and `Telegram.WebApp.initData` will require ngrok, Cloudflare Tunnel, or an equivalent service.

Integrated flow, closest to production:

```text
npm run build
terminal 1: ngrok http 8080
terminal 2: APP_BASE_URL=https://....ngrok.app go run ./cmd/bot
```

For frontend development with HMR:

```text
terminal 1: ngrok http 5173
terminal 2: APP_BASE_URL=https://....ngrok.app go run ./cmd/bot
terminal 3: npm run dev          # Vite on :5173, proxying /api to :8080
```

In this second mode, Vite must also proxy the required backend routes. When the temporary tunnel URL changes, `APP_BASE_URL` must be updated and, if Telegram/BotFather requires it for the chosen launch mode, so must the configured Mini App domain. A reserved domain from the tunnel provider would reduce this friction.

## Proposed data model

```sql
CREATE TABLE drafts (
    id UUID PRIMARY KEY,
    chat_id BIGINT NOT NULL,
    message_thread_id INTEGER NOT NULL DEFAULT 0,
    owner_telegram_id BIGINT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('email', 'whatsapp', 'generic')),
    subject TEXT,
    content_json JSONB NOT NULL,
    body_text TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'superseded')),
    revision INTEGER NOT NULL DEFAULT 1,
    telegram_message_id BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX drafts_one_active_per_conversation
    ON drafts (chat_id, message_thread_id)
    WHERE status = 'active';
```

No separate revision-history table will be created for the MVP. The product is expected to have a single primary user, concurrent editing is unlikely, and version recovery does not currently justify the additional schema, transactions, and test cases. That effort is better invested in the end-to-end editing flow, Telegram authentication, and a reliable mobile experience.

The `drafts.revision` counter is still required for optimistic concurrency. Updates will atomically replace the current content only when the client still has the latest revision:

```sql
UPDATE drafts
SET subject = $1,
    content_json = $2,
    body_text = $3,
    revision = revision + 1,
    updated_at = CURRENT_TIMESTAMP
WHERE id = $4
  AND revision = $5;
```

If no row is updated, the API will return `409 Conflict`. Full revision history can be added later if multiple editors, audit requirements, or version recovery create a concrete need for it; only changes made after that feature is introduced would be retained.

## Planned API

```text
GET   /healthz                  process is alive, without external dependencies
GET   /readyz                   checks PostgreSQL
GET   /miniapp                  Mini App shell
POST  /api/miniapp/session      validates initData and starts/authorizes the session
GET   /api/drafts/{id}          retrieves an authorized draft
PATCH /api/drafts/{id}          updates the current draft
GET   /api/drafts               outside the MVP; lists accessible drafts
```

The `PATCH` will include the expected revision:

```json
{
  "expected_revision": 3,
  "subject": "Visit proposal",
  "content": {}
}
```

If the current revision is no longer 3, the API will return `409 Conflict` with the current version. This prevents an old tab from silently overwriting a newer edit.

## Authentication and security

- The Mini App will send `Telegram.WebApp.initData` to the backend; `initDataUnsafe` will not be used as a trusted source.
- The backend will validate the signature with `TELEGRAM_BOT_TOKEN` and reject an `auth_date` that is too old.
- The validated user must exist in `allowed_users`.
- Every read and write will check authorization for the draft.
- The draft UUID will only be a reference, not a credential.
- Draft content will not be placed in the query string or `startapp`; only an opaque reference will be used.
- Size limits will be applied to the subject and document, alongside strict Tiptap JSON validation and appropriate security headers.
- Editor content will not be injected as arbitrary HTML.
- Requests and logs will not include complete draft bodies or `initData`.

## Implementation slices

Each slice must leave an executable and verifiable feature. The order reduces risk before adding the model to the flow.

### Slice 1 — Web runtime, health, and deployment ✅ Complete

**Demonstrable result:** the binary continues responding through the bot and also exposes an HTTPS URL with health checks.

- Add a Go HTTP server with `GET /healthz` and `GET /readyz`.
- Run HTTP and Telegram polling concurrently with coordinated shutdown.
- Add `PORT` and `APP_BASE_URL` to configuration, `.env.example`, and tests.
- Change `.do/app.yaml` from `workers` to `services`, declaring the port, ingress, and health check.
- Keep a single instance.
- Document both local flows with ngrok/a tunnel.
- Update the Dockerfile, deployment scripts, and README.

**Acceptance:**

- `curl localhost:8080/healthz` returns `200`.
- `/readyz` reflects whether PostgreSQL is available.
- The bot continues answering messages.
- The tunnel URL returns the health check.
- The DigitalOcean deployment is healthy as a `service`.

### Slice 2 — Embedded SolidJS/Vite shell ✅ Complete

**Demonstrable result:** Go serves a compiled Solid page from inside the binary.

- Create the SolidJS + TypeScript + Vite project under `web/`.
- Create a mobile-first screen that displays “Mini App connected”.
- Read Telegram's theme, viewport, and safe area when the SDK is present.
- Show a clear development mode when opened outside Telegram.
- Configure the Vite output and `go:embed`.
- Add the Node build to Docker and Make targets.
- Add an SPA fallback only under `/miniapp`, without capturing nonexistent API routes.

**Acceptance:**

- `npm run build` followed by `go build ./cmd/bot` produces a single functional binary.
- `/miniapp` works from the binary without Node installed at runtime.
- Versioned assets load under the same public URL.

### Slice 3 — Telegram button and authenticated handshake

**Demonstrable result:** a button sent by the bot opens the Mini App, which displays the authenticated user's identity, without drafts yet.

- Temporarily add an explicit command or trigger to send the **Open editor** button.
- Open the Mini App in a private conversation with `web_app`.
- Implement server-side validation of `Telegram.WebApp.initData` and the age of `auth_date`.
- Verify `allowed_users`.
- Establish the session/API mechanism for subsequent requests.
- Test the real flow in Telegram through ngrok.

**Acceptance:**

- Opening from Telegram identifies the correct user.
- Opening the API directly without valid credentials returns `401` or `403`.
- Manipulated or expired `initData` is rejected.
- The limitation of the `web_app` button to private chats and the `startapp` alternative for groups are documented.

### Slice 4 — Draft persistence, without the agent or editor

**Demonstrable result:** a draft can be created and retrieved from PostgreSQL, but it is not yet created by the model or edited in the Mini App.

- Add the `drafts` migration.
- Add models, repository, and domain service.
- Implement transactional creation and the single-active-draft invariant.
- Convert plain text into an initial Tiptap document.
- Add a development-only mechanism or an integration test to create a draft.
- Do not expose a generic public creation endpoint unless the product requires one.

**Acceptance:**

- Creating a draft generates revision 1.
- Creating another draft in the same conversation marks the previous one as `superseded`.
- Two concurrent creations do not leave two active drafts.
- Repository tests cover the transaction and unique index.

### Slice 5 — Open and view a real draft

**Demonstrable result:** the button associated with a draft opens the Mini App and displays its subject and content in read-only mode.

- Pass only the draft UUID/reference to the Mini App.
- Implement `GET /api/drafts/{id}` with authorization.
- Load the draft from Solid with loading, error, and unauthorized states.
- Initialize Tiptap in read-only mode.
- Verify that a user cannot open another user's draft.

**Acceptance:**

- The correct draft opens from the Telegram button.
- A nonexistent UUID returns `404`.
- An existing but unauthorized UUID does not leak content.

### Slice 6 — Editing and concurrency from the Mini App

**Demonstrable result:** the user can edit, save, close, and reopen the draft without losing changes.

- Enable the minimal Tiptap editor and toolbar.
- Implement `PATCH /api/drafts/{id}`.
- Validate the content schema and derive `body_text` on the server.
- Atomically update the draft and increment `revision` only when it matches `expected_revision`.
- Return `409 Conflict` when the expected revision is stale.
- Add dirty/saving/saved/error states and prevent accidental loss of changes.
- Integrate Telegram's main button for saving when useful.

**Acceptance:**

- An edit increments `drafts.revision` exactly once.
- Reopening displays the saved content.
- An update with an old revision does not overwrite the new one.
- Invalid JSON or disallowed nodes are rejected.

### Slice 7 — Draft creation by the agent

**Demonstrable result:** “Write me a WhatsApp message…” creates the draft in the database, and the bot responds with a preview and an **Edit** button.

- Add the native `create_draft(kind, subject?, body)` tool.
- Give tools a typed execution context containing the conversation and user; these IDs will not come from model arguments.
- Add prompt instructions describing when to create a new draft.
- Return canonical JSON from the tool.
- Make the handler detect the created draft, send the preview, and attach the correct button.
- Store `telegram_message_id` if the preview may be refreshed later.

**Acceptance:**

- A drafting request creates a draft with `revision = 1`, not just a message.
- The model cannot choose the owner or conversation.
- The button opens exactly the created draft.
- A normal response that does not request drafting creates no draft.

### Slice 8 — Active draft in context and editing by the agent

**Demonstrable result:** after a manual edit, “make it shorter” modifies the existing draft instead of creating another one.

- Load the active draft before every generation.
- Inject a JSON representation reconstructed from PostgreSQL into the context, without persisting this wrapper in `messages`.
- Add `update_draft(subject?, body, expected_revision)`.
- Apply agent updates through the same atomic revision check used by the Mini App.
- Update the preview/button after the change.
- Define in the prompt that a new draft is created only when the user explicitly requests one.

**Acceptance:**

- The agent immediately sees the latest edit made in the Mini App.
- Change instructions update the same UUID.
- An explicit request for a new text replaces the active draft.
- A stale agent update receives a conflict instead of overwriting a newer user edit.

### Slice 9 — Telegram preview synchronization

**Demonstrable result:** saving in the Mini App does not leave the bot message displaying an old version.

- If `telegram_message_id` exists, update the preview through the Bot API after the `PATCH`.
- Handle deleted or overly old messages without failing the save.
- Truncate or split previews that exceed Telegram limits.
- Always keep the **Edit** button.

**Acceptance:**

- Saving updates the preview whenever possible.
- A Telegram error does not undo an edit that has already been saved.
- The full content remains available in the Mini App even if the preview is truncated.

### Slice 10 — Listing and recovering previous drafts (post-MVP)

**Demonstrable result:** the user can view old drafts and reactivate or duplicate one.

- Implement a paginated and authorized `GET /api/drafts`.
- Add a list to the Mini App with type, excerpt, status, and date.
- Explicitly define whether “recovering” reactivates the same UUID or creates a new copy; creating a new copy is recommended to preserve the older draft.
- Add search only if actual volume justifies it.

This is not required to demonstrate the main collaborative flow.

## Possible later slices

- **Output actions:** copy plain text, open `mailto:`, or share to WhatsApp. No automatic sending without explicit confirmation.
- **Richer draft types:** recipient, CC/BCC, language, tone, and metadata specific to email or WhatsApp.
- **Revision history and recovery:** add historical snapshots, comparison, and restoration if a concrete need emerges.
- **Groups and topics:** `startapp` deep link, per-conversation authorization, and return to the correct topic.
- **Process separation:** bot worker and web service if the API needs to scale independently.
- **Observability:** metrics for creation, editing, conflicts, and errors, without logging sensitive content.
- **Mobile E2E:** frontend tests with a Telegram viewport; the real Telegram handshake will continue to require a smoke test through a tunnel.

## MVP order

The recommended critical path is:

```text
web/health
  → embedded shell
  → Telegram button + authentication
  → persistence
  → reading
  → editing
  → agent create_draft
  → context + update_draft
  → preview synchronization
```

Draft listing comes after this critical path.

## MVP definition of done

The MVP is complete when, from an authorized private chat:

1. the user asks to draft an email or WhatsApp message;
2. the agent creates a persistent draft and displays an **Edit** button;
3. the Mini App opens authenticated with the correct content;
4. the user edits and saves the draft;
5. the user asks the bot for a change;
6. the agent modifies the same draft based on the version edited by the user;
7. when the user explicitly asks for a new text, the previous draft is no longer active.
