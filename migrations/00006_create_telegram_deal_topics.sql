-- +goose Up
CREATE TABLE telegram_deal_topics (
    deal_id TEXT PRIMARY KEY,
    message_thread_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE telegram_deal_topics;
