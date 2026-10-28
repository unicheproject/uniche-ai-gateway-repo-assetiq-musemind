-- +goose Up
ALTER TABLE assets ADD COLUMN idempotency_key TEXT UNIQUE;

-- +goose Down
ALTER TABLE assets DROP COLUMN idempotency_key;
