CREATE TABLE media_assets (
    id uuid PRIMARY KEY,
    tree_id uuid NOT NULL REFERENCES family_trees (id) ON DELETE CASCADE,
    client_request_id uuid NOT NULL,
    kind text NOT NULL,
    status text NOT NULL,
    object_key text NOT NULL,
    original_filename text NOT NULL,
    mime_type text NOT NULL,
    size_bytes bigint NOT NULL,
    checksum_sha256 text NOT NULL,
    etag text NOT NULL DEFAULT '',
    width integer,
    height integer,
    caption text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    uploaded_by uuid NOT NULL,
    uploaded_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz,
    version integer NOT NULL DEFAULT 1,
    CONSTRAINT media_assets_tree_identity_uq UNIQUE (tree_id, id),
    CONSTRAINT media_assets_client_request_uq UNIQUE (tree_id, client_request_id),
    CONSTRAINT media_assets_object_key_uq UNIQUE (object_key),
    CONSTRAINT media_assets_kind_check
        CHECK (kind IN ('photo', 'document', 'other')),
    CONSTRAINT media_assets_status_check
        CHECK (status IN ('pending', 'uploaded', 'processing', 'ready', 'rejected', 'deleted')),
    CONSTRAINT media_assets_object_key_check
        CHECK (char_length(btrim(object_key)) BETWEEN 1 AND 1024),
    CONSTRAINT media_assets_original_filename_check
        CHECK (char_length(btrim(original_filename)) BETWEEN 1 AND 255),
    CONSTRAINT media_assets_mime_type_check
        CHECK (char_length(btrim(mime_type)) BETWEEN 1 AND 255),
    CONSTRAINT media_assets_size_check CHECK (size_bytes > 0),
    CONSTRAINT media_assets_checksum_check
        CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT media_assets_etag_check CHECK (char_length(etag) <= 255),
    CONSTRAINT media_assets_dimensions_check
        CHECK ((width IS NULL OR width > 0) AND (height IS NULL OR height > 0)),
    CONSTRAINT media_assets_caption_check CHECK (char_length(caption) <= 500),
    CONSTRAINT media_assets_description_check CHECK (char_length(description) <= 5000),
    CONSTRAINT media_assets_version_positive_check CHECK (version > 0)
);

CREATE INDEX media_assets_active_by_tree_idx
    ON media_assets (tree_id, status, created_at DESC, id)
    WHERE deleted_at IS NULL;

CREATE TABLE person_media (
    tree_id uuid NOT NULL,
    person_id uuid NOT NULL,
    media_id uuid NOT NULL,
    role text NOT NULL,
    sort_order integer NOT NULL DEFAULT 0,
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (person_id, media_id),
    CONSTRAINT person_media_person_fk
        FOREIGN KEY (tree_id, person_id)
        REFERENCES persons (tree_id, id)
        ON DELETE CASCADE,
    CONSTRAINT person_media_asset_fk
        FOREIGN KEY (tree_id, media_id)
        REFERENCES media_assets (tree_id, id)
        ON DELETE CASCADE,
    CONSTRAINT person_media_role_check
        CHECK (role IN ('profile', 'gallery', 'document', 'other')),
    CONSTRAINT person_media_sort_order_check
        CHECK (sort_order BETWEEN 0 AND 1000000)
);

CREATE INDEX person_media_by_asset_idx
    ON person_media (tree_id, media_id, person_id);

ALTER TABLE persons
    ADD CONSTRAINT persons_primary_media_fk
    FOREIGN KEY (tree_id, primary_media_id)
    REFERENCES media_assets (tree_id, id);
