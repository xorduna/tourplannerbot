-- +goose Up
CREATE TABLE llm_requests (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    source_message_id BIGINT REFERENCES messages(id),
    chat_id BIGINT NOT NULL,
    message_thread_id INTEGER NOT NULL DEFAULT 0,
    telegram_user_id BIGINT,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    operation TEXT NOT NULL DEFAULT 'response',
    provider_response_id TEXT,
    status TEXT NOT NULL CHECK (status IN ('succeeded', 'failed')),
    duration_ms INTEGER NOT NULL,
    input_tokens BIGINT,
    cached_input_tokens BIGINT,
    output_tokens BIGINT,
    reasoning_tokens BIGINT,
    total_tokens BIGINT,
    estimated_cost_micro_usd BIGINT,
    pricing_version TEXT,
    error_message TEXT
);

CREATE INDEX llm_requests_created_at_index ON llm_requests (created_at DESC);
CREATE INDEX llm_requests_conversation_created_at_index ON llm_requests (chat_id, message_thread_id, created_at DESC);
CREATE INDEX llm_requests_model_created_at_index ON llm_requests (model, created_at DESC);

-- +goose Down
DROP TABLE llm_requests;
