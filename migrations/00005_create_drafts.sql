-- +goose Up
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

-- +goose Down
DROP TABLE drafts;
