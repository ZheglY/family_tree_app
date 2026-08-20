CREATE TABLE family_trees (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    owner_user_id uuid NOT NULL,
    root_person_id uuid,
    cover_media_id uuid,
    privacy text NOT NULL DEFAULT 'private',
    locale text NOT NULL,
    timezone text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz,
    version integer NOT NULL DEFAULT 1,
    CONSTRAINT family_trees_name_check
        CHECK (char_length(btrim(name)) BETWEEN 1 AND 150),
    CONSTRAINT family_trees_description_check
        CHECK (char_length(description) <= 5000),
    CONSTRAINT family_trees_privacy_check
        CHECK (privacy = 'private'),
    CONSTRAINT family_trees_locale_check
        CHECK (char_length(locale) BETWEEN 2 AND 35),
    CONSTRAINT family_trees_timezone_check
        CHECK (char_length(timezone) BETWEEN 1 AND 100),
    CONSTRAINT family_trees_version_positive_check
        CHECK (version > 0)
);

CREATE TABLE tree_members (
    tree_id uuid NOT NULL REFERENCES family_trees (id) ON DELETE CASCADE,
    user_id uuid NOT NULL,
    role text NOT NULL,
    status text NOT NULL,
    invited_by uuid,
    created_at timestamptz NOT NULL,
    accepted_at timestamptz,
    PRIMARY KEY (tree_id, user_id),
    CONSTRAINT tree_members_role_check
        CHECK (role IN ('owner', 'editor', 'viewer')),
    CONSTRAINT tree_members_status_check
        CHECK (status IN ('invited', 'active', 'revoked')),
    CONSTRAINT tree_members_active_accepted_check
        CHECK (status <> 'active' OR accepted_at IS NOT NULL)
);

CREATE UNIQUE INDEX tree_members_one_active_owner_uq
    ON tree_members (tree_id)
    WHERE role = 'owner' AND status = 'active';

CREATE INDEX tree_members_access_by_user_idx
    ON tree_members (user_id, status, tree_id);

CREATE INDEX family_trees_active_by_owner_idx
    ON family_trees (owner_user_id, updated_at DESC)
    WHERE deleted_at IS NULL;

CREATE TABLE audit_log (
    id uuid PRIMARY KEY,
    tree_id uuid REFERENCES family_trees (id) ON DELETE SET NULL,
    actor_user_id uuid,
    action text NOT NULL,
    entity_type text NOT NULL,
    entity_id uuid,
    request_id text NOT NULL DEFAULT '',
    ip_address inet,
    changes jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL
);

CREATE INDEX audit_log_tree_created_idx
    ON audit_log (tree_id, created_at DESC);
