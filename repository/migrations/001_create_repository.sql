-- +goose Up
CREATE TABLE assets (
    id                 TEXT PRIMARY KEY,
    owner_id           TEXT,
    visibility         TEXT        NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'private')),
    read_only          BOOLEAN     NOT NULL DEFAULT FALSE,
    source             TEXT        NOT NULL CHECK (source IN ('eccch', 'europeana', 'local', 'partner')),
    current_version_id TEXT,
    withdrawn_at       TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE asset_versions (
    id          TEXT PRIMARY KEY,
    seq         BIGSERIAL   NOT NULL UNIQUE,
    asset_id    TEXT        NOT NULL REFERENCES assets (id),
    parent_id   TEXT REFERENCES asset_versions (id),
    title       TEXT,
    description TEXT,
    type        TEXT,
    creator     TEXT,
    date        TEXT,
    location    TEXT,
    rights      TEXT,
    raw_source  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX asset_versions_asset_id_idx ON asset_versions (asset_id, seq DESC);

ALTER TABLE assets
    ADD CONSTRAINT assets_current_version_id_fkey
    FOREIGN KEY (current_version_id) REFERENCES asset_versions (id);

CREATE TABLE asset_files (
    id             TEXT PRIMARY KEY,
    upload_id      TEXT        NOT NULL UNIQUE,
    version_id     TEXT        NOT NULL REFERENCES asset_versions (id),
    media_type     TEXT        NOT NULL,
    role           TEXT        NOT NULL CHECK (role IN ('primary', 'thumbnail', 'documentation')),
    status         TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'available')),
    sha256         TEXT        NOT NULL,
    size_bytes     BIGINT      NOT NULL,
    bytes_received BIGINT      NOT NULL DEFAULT 0,
    path           TEXT,
    registered_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX asset_files_version_id_idx ON asset_files (version_id);

CREATE TABLE asset_external_ids (
    asset_id   TEXT NOT NULL REFERENCES assets (id),
    provider   TEXT NOT NULL,
    identifier TEXT NOT NULL,
    PRIMARY KEY (asset_id, provider),
    UNIQUE (provider, identifier)
);

CREATE TABLE asset_relations (
    source_id TEXT NOT NULL REFERENCES assets (id),
    target_id TEXT NOT NULL REFERENCES assets (id),
    relation  TEXT NOT NULL,
    PRIMARY KEY (source_id, target_id, relation)
);

-- +goose Down
DROP TABLE asset_relations;
DROP TABLE asset_external_ids;
DROP TABLE asset_files;
ALTER TABLE assets DROP CONSTRAINT assets_current_version_id_fkey;
DROP TABLE asset_versions;
DROP TABLE assets;
