-- +goose Up
ALTER TABLE messages
    DROP CONSTRAINT messages_role_check,
    ADD COLUMN tool_call_id TEXT,
    ADD COLUMN tool_name TEXT,
    ADD COLUMN tool_arguments JSONB,
    ADD CONSTRAINT messages_role_check CHECK (role IN ('user', 'assistant', 'tool', 'reasoning'));

CREATE INDEX messages_tool_call_id_index
    ON messages (tool_call_id)
    WHERE tool_call_id IS NOT NULL;

-- +goose Down
DROP INDEX messages_tool_call_id_index;

DELETE FROM messages
WHERE role IN ('tool', 'reasoning') OR tool_call_id IS NOT NULL;

ALTER TABLE messages
    DROP CONSTRAINT messages_role_check,
    DROP COLUMN tool_arguments,
    DROP COLUMN tool_name,
    DROP COLUMN tool_call_id,
    ADD CONSTRAINT messages_role_check CHECK (role IN ('user', 'assistant'));
