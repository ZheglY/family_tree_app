CREATE TABLE persons (
    id uuid PRIMARY KEY,
    tree_id uuid NOT NULL REFERENCES family_trees (id) ON DELETE CASCADE,
    sex text NOT NULL,
    life_status text NOT NULL,
    biography text NOT NULL DEFAULT '',
    notes text NOT NULL DEFAULT '',
    primary_media_id uuid,
    privacy_level text NOT NULL DEFAULT 'tree_members',
    created_by uuid NOT NULL,
    updated_by uuid NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz,
    version integer NOT NULL DEFAULT 1,
    CONSTRAINT persons_tree_identity_uq UNIQUE (tree_id, id),
    CONSTRAINT persons_sex_check
        CHECK (sex IN ('male', 'female', 'unknown', 'not_specified')),
    CONSTRAINT persons_life_status_check
        CHECK (life_status IN ('alive', 'deceased', 'unknown')),
    CONSTRAINT persons_biography_check
        CHECK (char_length(biography) <= 50000),
    CONSTRAINT persons_notes_check
        CHECK (char_length(notes) <= 20000),
    CONSTRAINT persons_privacy_level_check
        CHECK (privacy_level = 'tree_members'),
    CONSTRAINT persons_version_positive_check
        CHECK (version > 0)
);

CREATE INDEX persons_active_by_tree_idx
    ON persons (tree_id, updated_at DESC, id)
    WHERE deleted_at IS NULL;

CREATE TABLE person_names (
    id uuid PRIMARY KEY,
    person_id uuid NOT NULL,
    tree_id uuid NOT NULL,
    type text NOT NULL,
    given_name text NOT NULL DEFAULT '',
    patronymic text NOT NULL DEFAULT '',
    family_name text NOT NULL DEFAULT '',
    prefix text NOT NULL DEFAULT '',
    suffix text NOT NULL DEFAULT '',
    full_text text NOT NULL,
    is_preferred boolean NOT NULL DEFAULT false,
    language_code text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT person_names_person_fk
        FOREIGN KEY (tree_id, person_id)
        REFERENCES persons (tree_id, id)
        ON DELETE CASCADE,
    CONSTRAINT person_names_type_check
        CHECK (type IN ('primary', 'birth', 'married', 'alias', 'transliteration', 'other')),
    CONSTRAINT person_names_given_name_check CHECK (char_length(given_name) <= 150),
    CONSTRAINT person_names_patronymic_check CHECK (char_length(patronymic) <= 150),
    CONSTRAINT person_names_family_name_check CHECK (char_length(family_name) <= 150),
    CONSTRAINT person_names_prefix_check CHECK (char_length(prefix) <= 50),
    CONSTRAINT person_names_suffix_check CHECK (char_length(suffix) <= 50),
    CONSTRAINT person_names_full_text_check CHECK (char_length(btrim(full_text)) BETWEEN 1 AND 700),
    CONSTRAINT person_names_language_code_check CHECK (char_length(language_code) BETWEEN 2 AND 35)
);

CREATE UNIQUE INDEX person_names_one_preferred_uq
    ON person_names (person_id)
    WHERE is_preferred;

CREATE INDEX person_names_preferred_search_idx
    ON person_names (tree_id, full_text, person_id)
    WHERE is_preferred;
