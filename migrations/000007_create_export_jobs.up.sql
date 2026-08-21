CREATE TABLE export_jobs (
    id uuid PRIMARY KEY,
    tree_id uuid NOT NULL REFERENCES family_trees (id) ON DELETE CASCADE,
    client_request_id uuid NOT NULL,
    requested_by uuid NOT NULL,
    format text NOT NULL,
    schema_version integer NOT NULL,
    parameters jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL,
    progress integer NOT NULL DEFAULT 0,
    result_object_key text NOT NULL DEFAULT '',
    result_mime_type text NOT NULL DEFAULT '',
    result_size_bytes bigint NOT NULL DEFAULT 0,
    result_checksum_sha256 text NOT NULL DEFAULT '',
    error_code text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    started_at timestamptz,
    finished_at timestamptz,
    expires_at timestamptz,
    CONSTRAINT export_jobs_tree_identity_uq UNIQUE (tree_id, id),
    CONSTRAINT export_jobs_idempotency_uq
        UNIQUE (tree_id, requested_by, client_request_id),
    CONSTRAINT export_jobs_format_check CHECK (format = 'json_backup'),
    CONSTRAINT export_jobs_schema_version_check CHECK (schema_version > 0),
    CONSTRAINT export_jobs_status_check
        CHECK (status IN ('queued', 'running', 'completed', 'failed', 'expired')),
    CONSTRAINT export_jobs_progress_check CHECK (progress BETWEEN 0 AND 100),
    CONSTRAINT export_jobs_object_key_check
        CHECK (result_object_key = '' OR char_length(result_object_key) BETWEEN 1 AND 1024),
    CONSTRAINT export_jobs_result_size_check CHECK (result_size_bytes >= 0),
    CONSTRAINT export_jobs_checksum_check
        CHECK (result_checksum_sha256 = '' OR result_checksum_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT export_jobs_error_code_check CHECK (char_length(error_code) <= 100),
    CONSTRAINT export_jobs_completed_result_check
        CHECK (
            status <> 'completed' OR (
                progress = 100 AND
                result_object_key <> '' AND
                result_mime_type = 'application/json' AND
                result_size_bytes > 0 AND
                result_checksum_sha256 <> '' AND
                finished_at IS NOT NULL AND
                expires_at IS NOT NULL
            )
        )
);

CREATE UNIQUE INDEX export_jobs_result_object_uq
    ON export_jobs (result_object_key)
    WHERE result_object_key <> '';

CREATE INDEX export_jobs_history_idx
    ON export_jobs (tree_id, requested_by, created_at DESC, id DESC);

CREATE INDEX export_jobs_expiration_idx
    ON export_jobs (expires_at, id)
    WHERE status = 'completed';
