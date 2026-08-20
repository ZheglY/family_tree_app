CREATE TABLE family_unions (
    id uuid PRIMARY KEY,
    tree_id uuid NOT NULL REFERENCES family_trees (id) ON DELETE CASCADE,
    type text NOT NULL,
    end_reason text NOT NULL DEFAULT '',
    note text NOT NULL DEFAULT '',
    created_by uuid NOT NULL,
    updated_by uuid NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz,
    version integer NOT NULL DEFAULT 1,
    CONSTRAINT family_unions_tree_identity_uq UNIQUE (tree_id, id),
    CONSTRAINT family_unions_type_check
        CHECK (type IN ('marriage', 'civil_union', 'partnership', 'engagement', 'unknown')),
    CONSTRAINT family_unions_end_reason_check
        CHECK (char_length(end_reason) <= 500),
    CONSTRAINT family_unions_note_check
        CHECK (char_length(note) <= 5000),
    CONSTRAINT family_unions_version_positive_check
        CHECK (version > 0)
);

CREATE INDEX family_unions_active_by_tree_idx
    ON family_unions (tree_id, updated_at DESC, id)
    WHERE deleted_at IS NULL;

CREATE TABLE union_members (
    union_id uuid NOT NULL,
    person_id uuid NOT NULL,
    tree_id uuid NOT NULL,
    role text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    PRIMARY KEY (union_id, person_id),
    CONSTRAINT union_members_union_fk
        FOREIGN KEY (tree_id, union_id)
        REFERENCES family_unions (tree_id, id)
        ON DELETE CASCADE,
    CONSTRAINT union_members_person_fk
        FOREIGN KEY (tree_id, person_id)
        REFERENCES persons (tree_id, id)
        ON DELETE CASCADE,
    CONSTRAINT union_members_role_check
        CHECK (char_length(role) <= 50)
);

CREATE INDEX union_members_by_person_idx
    ON union_members (tree_id, person_id, union_id);
