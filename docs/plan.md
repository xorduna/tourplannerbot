# Travel Bot — Project Prompt

## Overview

Telegram bot in Go for Diana, a licensed Barcelona tour guide. The bot is her internal tool to plan trips for clients and answer quick queries. It uses OpenAI's API (with the option to swap providers later since the interface is OpenAI-compatible) and has a tool-calling system for real-time information lookup.

## Two Operating Modes

### Group Chat = Client Trip
Diana creates a Telegram group per client, invites the bot. She describes the client ("retired couple, 3 days, interested in Gaudí and food") and the bot maintains full context for that trip. One group = one trip = one client.

### Private Chat = Quick Queries
Diana messages the bot directly for fast lookups with no client context. "What time does Sagrada Família close on Saturdays?" — answer and done.

The bot distinguishes modes by chat type: `private` → query mode, `group`/`supergroup` → trip mode with persistent context.

## Authentication

PIN-based access. Single PIN stored as environment variable. Flow:

1. Unknown user sends any message → bot replies "Introdueix el PIN d'accés"
2. User sends correct PIN → bot adds their Telegram `user_id` to `allowed_users`, replies "✓ Accés concedit"
3. From now on, bot responds normally to this user
4. Wrong PIN → "PIN incorrecte"

Users stay authorized even if PIN changes. Only new users need the current PIN.

## Tech Stack

- **Language**: Go 1.22+
- **Telegram**: `github.com/go-telegram/bot`
- **LLM**: OpenAI API (tool/function calling). Interface should be generic enough to swap to OpenRouter or Anthropic later.
- **Database**: PostgreSQL (existing instance)
- **Deployment**: Docker on DigitalOcean App Platform (manually created app, secrets as env vars)
- **Logging**: `slog` (structured)

## Project Structure

```
cmd/bot/main.go
internal/
  telegram/
    handler.go           # Message routing (auth, group vs private)
    auth.go              # PIN verification, user whitelist
  llm/
    client.go            # OpenAI-compatible client
    types.go             # Message, Tool, ToolCall, Response
  tools/
    registry.go          # Register/lookup/execute tools
    types.go             # Tool + ToolHandler interfaces
    monuments.go         # Tool: monument info/availability
    restaurants.go       # Tool: find restaurants
    weather.go           # Tool: weather forecast
  trip/
    service.go           # Trip lifecycle (create, load, update summary)
    repository.go        # Postgres queries for trips
  conversation/
    service.go           # Load/save messages, context window mgmt
    repository.go        # Postgres queries for messages
  config/
    config.go            # Env var loading
prompts/
  system_trip.md         # System prompt for trip mode (group chats)
  system_query.md        # System prompt for query mode (private chat)
  skills/
    monuments.md         # Knowledge about BCN monuments
    restaurants.md       # Restaurant recommendation rules
    itineraries.md       # How to build itineraries
    practical_info.md    # Transport, tips, logistics
Dockerfile
docker-compose.yml       # For local dev (bot + postgres)
.env.example
```

## Database Schema

```sql
CREATE TABLE allowed_users (
    telegram_id  BIGINT PRIMARY KEY,
    name         TEXT,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE trips (
    id          SERIAL PRIMARY KEY,
    chat_id     BIGINT UNIQUE NOT NULL,
    summary     TEXT DEFAULT '',
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE messages (
    id          SERIAL PRIMARY KEY,
    chat_id     BIGINT NOT NULL,
    user_id     BIGINT,                   -- Telegram user ID (who sent it)
    role        TEXT NOT NULL,             -- user, assistant, tool
    content     TEXT NOT NULL,
    tool_calls  JSONB,
    tool_name   TEXT,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_messages_chat_id ON messages(chat_id, created_at);
```

## Core Interfaces

```go
// internal/llm/types.go

type Message struct {
    Role       string     `json:"role"`
    Content    string     `json:"content"`
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
    ToolCallID string     `json:"tool_call_id,omitempty"`
    Name       string     `json:"name,omitempty"`
}

type ToolCall struct {
    ID       string `json:"id"`
    Type     string `json:"type"`
    Function struct {
        Name      string `json:"name"`
        Arguments string `json:"arguments"`
    } `json:"function"`
}

type Response struct {
    Content   string
    ToolCalls []ToolCall
}

type Client interface {
    Chat(ctx context.Context, messages []Message, tools []Tool) (*Response, error)
}
```

```go
// internal/tools/types.go

type Tool struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`
    Parameters  json.RawMessage `json:"parameters"` // JSON Schema
}

type ToolHandler func(ctx context.Context, args json.RawMessage) (string, error)

type Registry interface {
    Register(tool Tool, handler ToolHandler)
    List() []Tool
    Execute(ctx context.Context, name string, args json.RawMessage) (string, error)
}
```

## Tool Calling Loop

```
messages = load_history(chat_id) + new_user_message
tools = registry.List()

for i := 0; i < MAX_ITERATIONS; i++ {
    response = llm.Chat(messages, tools)

    if len(response.ToolCalls) == 0 {
        return response.Content
    }

    append assistant message with tool_calls
    for each tool_call:
        result = registry.Execute(tool_call.name, tool_call.args)
        append tool result message
}

return "Sorry, I couldn't complete the request"
```

Max iterations: 10.

## Prompt Loading

On startup, the bot reads `prompts/` from the filesystem:
- `system_trip.md` — loaded for group chat conversations
- `system_query.md` — loaded for private chat conversations
- `skills/*.md` — all loaded and appended to the system prompt

Diana can edit these markdown files to tune the bot's behavior without touching code. In Docker, mount `prompts/` as a volume.

### Default system_trip.md

```markdown
You are Diana's travel planning assistant. Diana is a licensed tour guide in Barcelona.

She is using you to plan trips for her clients. You are in a group chat dedicated to one client/trip.

Your job:
- Help Diana build an itinerary based on the client's profile
- Check availability and practical info using your tools
- Suggest activities, restaurants, and logistics
- Keep track of what's been decided

Be concise. This is a chat, not a report.
When you have enough info, propose a concrete plan rather than asking more questions.
Respond in the same language Diana uses.
```

### Default system_query.md

```markdown
You are Diana's quick-lookup assistant. Diana is a licensed tour guide in Barcelona.

Answer her questions directly and concisely. Use tools when you need real-time info.
No client context here — just fast, accurate answers.
Respond in the same language Diana uses.
```

## Configuration (.env.example)

```env
TELEGRAM_BOT_TOKEN=
OPENAI_API_KEY=
OPENAI_MODEL=gpt-4o-mini
OPENAI_BASE_URL=https://api.openai.com/v1
LLM_MAX_TOKENS=2048
TOOL_CALL_MAX_ITERATIONS=10
DATABASE_URL=postgres://user:pass@host:5432/travelbot?sslmode=require
ACCESS_PIN=1234
LOG_LEVEL=info
```

Note: `OPENAI_BASE_URL` makes it trivial to switch to OpenRouter or any compatible endpoint later.

## Dockerfile

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o bot ./cmd/bot

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/bot .
COPY prompts/ ./prompts/
CMD ["./bot"]
```

docker-compose.yml for local dev includes both the bot and a postgres container.

## Deployment

DigitalOcean App Platform. The app is created manually in the DO console:
- Source: GitHub repo (auto-deploy on push)
- Component type: Worker (not web — the bot uses polling, no incoming HTTP)
- Environment variables set as secrets in DO console:
  - `TELEGRAM_BOT_TOKEN`
  - `OPENAI_API_KEY`
  - `DATABASE_URL` (DO managed Postgres or external)
  - `ACCESS_PIN`
  - `OPENAI_MODEL`
  - `LOG_LEVEL`

---

## Implementation Plan — Vertical Slices

Each slice delivers working functionality top to bottom.
Each one is a single task for Claude Code / Copilot.

### Slice 1 — Echo Bot + Infra + Deploy
- Go project scaffold (`go mod init`, directory structure)
- Config loading from env vars
- Telegram bot with long polling that echoes messages back
- Dockerfile + docker-compose.yml (bot + postgres for local dev)
- Graceful shutdown on SIGTERM
- Push to GitHub, deploy to DigitalOcean App Platform
- **Done when**: Bot runs on DO, you message it on Telegram, it echoes back

### Slice 2 — Auth (PIN)
- Postgres connection + migration (create `allowed_users` table)
- PIN verification flow: unknown user → ask PIN → validate → add to whitelist
- Bot ignores messages from non-authorized users (except PIN input)
- **Done when**: New user must enter PIN before bot responds. After PIN, works normally.

### Slice 3 — LLM Integration
- OpenAI client implementing the `Client` interface
- Bot sends user message to OpenAI, returns the response
- Hardcoded system prompt for now
- No history, no tools, no distinction between group/private
- **Done when**: Authorized user messages the bot, gets an LLM-generated answer

### Slice 4 — Conversation History
- Create `messages` table
- Save every message (user + assistant) per `chat_id` with `user_id`
- Load last N messages when a new message arrives, send as context to LLM
- **Done when**: The bot remembers what you said earlier in the same chat

### Slice 5 — Group vs Private Mode
- Detect chat type from Telegram update
- Group: create trip record if new `chat_id`, load trip context, use trip system prompt
- Private: no trip, use query system prompt
- Auto-generate/update trip summary after each group conversation
- **Done when**: Bot behaves differently in group vs private chat, trip record exists in DB

### Slice 6 — Tool Calling (first tool)
- Tool registry implementation
- Tool calling loop (LLM → tool → LLM → response)
- One stub tool: `get_monument_info` (hardcoded BCN data)
- Save tool calls and results in message history
- **Done when**: Ask "what time does Park Güell close?" and the bot calls the tool and answers

### Slice 7 — Editable Prompts
- Load system prompts and skills from `prompts/` filesystem
- Append all `skills/*.md` to system prompt
- Reload on bot restart
- Write initial skill files with BCN knowledge
- **Done when**: Edit a .md file, restart bot, behavior changes

### Slice 8+ — More Tools (one per slice)
- Each tool is independent: implement handler, register, done
- Candidates: `find_restaurants`, `get_weather`, `check_availability`
- Connect to real APIs as needed (separate Python scraper service, Google Places, etc.)

### Future (not now)
- Web dashboard to view trips and conversations
- PDF itinerary export
- Client-facing features