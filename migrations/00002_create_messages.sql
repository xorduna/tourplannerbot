-- +goose Up
CREATE TABLE messages (
    id BIGSERIAL PRIMARY KEY,
    chat_id BIGINT NOT NULL,
    message_thread_id INTEGER NOT NULL DEFAULT 0,
    user_id BIGINT,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX messages_conversation_created_at_index
    ON messages (chat_id, message_thread_id, created_at DESC, id DESC);

-- +goose Down
DROP TABLE messages;
