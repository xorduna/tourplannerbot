-- +goose Up
CREATE TABLE allowed_users (
    telegram_id BIGINT PRIMARY KEY,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE allowed_users;
