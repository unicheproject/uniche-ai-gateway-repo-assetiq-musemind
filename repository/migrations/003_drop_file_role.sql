-- +goose Up
ALTER TABLE asset_files DROP COLUMN role;

-- +goose Down
ALTER TABLE asset_files ADD COLUMN role TEXT NOT NULL DEFAULT 'primary'
    CHECK (role IN ('primary', 'thumbnail', 'documentation'));
