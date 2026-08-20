CREATE TABLE parent_child_relations (
    id uuid PRIMARY KEY,
    tree_id uuid NOT NULL REFERENCES family_trees (id) ON DELETE CASCADE,
    parent_person_id uuid NOT NULL,
    child_person_id uuid NOT NULL,
    relation_type text NOT NULL,
    confidence text NOT NULL,
    note text NOT NULL DEFAULT '',
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz,
    version integer NOT NULL DEFAULT 1,
    CONSTRAINT parent_child_relations_tree_identity_uq UNIQUE (tree_id, id),
    CONSTRAINT parent_child_relations_parent_fk
        FOREIGN KEY (tree_id, parent_person_id)
        REFERENCES persons (tree_id, id)
        ON DELETE CASCADE,
    CONSTRAINT parent_child_relations_child_fk
        FOREIGN KEY (tree_id, child_person_id)
        REFERENCES persons (tree_id, id)
        ON DELETE CASCADE,
    CONSTRAINT parent_child_relations_not_self_check
        CHECK (parent_person_id <> child_person_id),
    CONSTRAINT parent_child_relations_type_check
        CHECK (relation_type IN ('biological', 'adoptive', 'foster', 'guardian', 'step', 'unknown')),
    CONSTRAINT parent_child_relations_confidence_check
        CHECK (confidence IN ('unverified', 'probable', 'confirmed', 'disputed')),
    CONSTRAINT parent_child_relations_note_check
        CHECK (char_length(note) <= 5000),
    CONSTRAINT parent_child_relations_version_positive_check
        CHECK (version > 0)
);

CREATE UNIQUE INDEX parent_child_relations_active_uq
    ON parent_child_relations (
        tree_id,
        parent_person_id,
        child_person_id,
        relation_type
    )
    WHERE deleted_at IS NULL;

CREATE INDEX parent_child_relations_active_parent_idx
    ON parent_child_relations (tree_id, parent_person_id, child_person_id)
    WHERE deleted_at IS NULL;

CREATE INDEX parent_child_relations_active_child_idx
    ON parent_child_relations (tree_id, child_person_id, parent_person_id)
    WHERE deleted_at IS NULL;
