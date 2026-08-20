CREATE TABLE background_jobs (
    id uuid PRIMARY KEY,
    kind text NOT NULL,
    deduplication_key text,
    payload jsonb NOT NULL,
    status text NOT NULL DEFAULT 'queued',
    attempts integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 5,
    available_at timestamptz NOT NULL,
    lease_expires_at timestamptz,
    heartbeat_at timestamptz,
    locked_by text,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    completed_at timestamptz,
    CONSTRAINT background_jobs_kind_check
        CHECK (char_length(btrim(kind)) BETWEEN 1 AND 100),
    CONSTRAINT background_jobs_deduplication_key_check
        CHECK (deduplication_key IS NULL OR char_length(deduplication_key) BETWEEN 1 AND 255),
    CONSTRAINT background_jobs_status_check
        CHECK (status IN ('queued', 'running', 'failed', 'succeeded', 'dead')),
    CONSTRAINT background_jobs_attempts_check
        CHECK (attempts >= 0 AND max_attempts BETWEEN 1 AND 100 AND attempts <= max_attempts),
    CONSTRAINT background_jobs_lease_check
        CHECK (
            (status = 'running' AND lease_expires_at IS NOT NULL AND locked_by IS NOT NULL) OR
            (status <> 'running' AND lease_expires_at IS NULL AND locked_by IS NULL)
        ),
    CONSTRAINT background_jobs_completed_check
        CHECK (
            (status IN ('succeeded', 'dead') AND completed_at IS NOT NULL) OR
            (status NOT IN ('succeeded', 'dead') AND completed_at IS NULL)
        )
);

CREATE UNIQUE INDEX background_jobs_deduplication_uq
    ON background_jobs (kind, deduplication_key)
    WHERE deduplication_key IS NOT NULL;

CREATE INDEX background_jobs_claim_idx
    ON background_jobs (available_at, created_at, id)
    WHERE status IN ('queued', 'failed', 'running');

CREATE TABLE media_variants (
    id uuid PRIMARY KEY,
    tree_id uuid NOT NULL,
    media_id uuid NOT NULL,
    kind text NOT NULL,
    object_key text NOT NULL,
    mime_type text NOT NULL,
    size_bytes bigint NOT NULL,
    checksum_sha256 text NOT NULL,
    width integer NOT NULL,
    height integer NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT media_variants_tree_identity_uq UNIQUE (tree_id, id),
    CONSTRAINT media_variants_asset_fk
        FOREIGN KEY (tree_id, media_id)
        REFERENCES media_assets (tree_id, id)
        ON DELETE CASCADE,
    CONSTRAINT media_variants_asset_kind_uq UNIQUE (media_id, kind),
    CONSTRAINT media_variants_object_key_uq UNIQUE (object_key),
    CONSTRAINT media_variants_kind_check
        CHECK (kind IN ('thumbnail', 'preview')),
    CONSTRAINT media_variants_object_key_check
        CHECK (char_length(btrim(object_key)) BETWEEN 1 AND 1024),
    CONSTRAINT media_variants_mime_type_check
        CHECK (mime_type IN ('image/jpeg', 'image/png')),
    CONSTRAINT media_variants_size_check CHECK (size_bytes > 0),
    CONSTRAINT media_variants_checksum_check
        CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT media_variants_dimensions_check CHECK (width > 0 AND height > 0)
);

CREATE INDEX media_variants_by_tree_asset_idx
    ON media_variants (tree_id, media_id, kind);

ALTER TABLE media_assets
    ADD COLUMN processing_error text NOT NULL DEFAULT '',
    ADD COLUMN processed_at timestamptz,
    ADD CONSTRAINT media_assets_processing_error_check
        CHECK (char_length(processing_error) <= 1000);
