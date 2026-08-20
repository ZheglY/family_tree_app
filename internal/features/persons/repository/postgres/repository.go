package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/ZheglY/family_tree_app/internal/features/persons/domain"
	"github.com/ZheglY/family_tree_app/internal/features/persons/service"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) CreateEditable(
	ctx context.Context,
	actorUserID uuid.UUID,
	card domain.Card,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin person creation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, card.Person.TreeID, actorUserID); err != nil {
		return err
	}
	person := card.Person
	if _, err := tx.Exec(ctx, `
		INSERT INTO persons (
			id, tree_id, sex, life_status, biography, notes,
			primary_media_id, privacy_level, created_by, updated_by,
			created_at, updated_at, deleted_at, version
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $9, $10, $10, NULL, $11
		)
	`,
		person.ID,
		person.TreeID,
		person.Sex,
		person.LifeStatus,
		person.Biography,
		person.Notes,
		person.PrimaryMediaID,
		person.PrivacyLevel,
		actorUserID,
		person.CreatedAt,
		person.Version,
	); err != nil {
		return fmt.Errorf("insert person: %w", err)
	}
	if err := insertPreferredName(ctx, tx, card.PreferredName); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit person creation transaction: %w", err)
	}
	return nil
}

func (r *Repository) ListAccessible(
	ctx context.Context,
	filter service.ListFilter,
) ([]domain.Card, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			p.id, p.tree_id, p.sex, p.life_status, p.biography, p.notes,
			p.primary_media_id, p.privacy_level, p.created_by, p.updated_by,
			p.created_at, p.updated_at, p.deleted_at, p.version,
			n.id, n.person_id, n.tree_id, n.type, n.given_name,
			n.patronymic, n.family_name, n.prefix, n.suffix, n.full_text,
			n.is_preferred, n.language_code, n.created_at, n.updated_at
		FROM persons p
		JOIN person_names n
		  ON n.person_id = p.id
		 AND n.tree_id = p.tree_id
		 AND n.is_preferred
		JOIN tree_members m
		  ON m.tree_id = p.tree_id
		 AND m.user_id = $2
		 AND m.status = 'active'
		JOIN family_trees t
		  ON t.id = p.tree_id
		 AND t.deleted_at IS NULL
		WHERE p.tree_id = $1
		  AND p.deleted_at IS NULL
		  AND ($3 = '' OR position(lower($3) in lower(n.full_text)) > 0)
		  AND ($4 = '' OR p.life_status = $4)
		  AND ($5::boolean IS NULL OR (p.primary_media_id IS NOT NULL) = $5)
		  AND ($6 = '' OR (n.full_text, p.id) > ($6, $7))
		ORDER BY n.full_text, p.id
		LIMIT $8
	`,
		filter.TreeID,
		filter.ActorUserID,
		filter.Query,
		filter.LifeStatus,
		filter.HasMedia,
		filter.AfterName,
		filter.AfterPersonID,
		filter.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list accessible persons: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Card, 0)
	for rows.Next() {
		card, err := scanCard(rows)
		if err != nil {
			return nil, fmt.Errorf("scan person list item: %w", err)
		}
		items = append(items, card)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accessible persons: %w", err)
	}
	return items, nil
}

func (r *Repository) GetAccessible(
	ctx context.Context,
	treeID uuid.UUID,
	personID uuid.UUID,
	actorUserID uuid.UUID,
	includeDeleted bool,
) (domain.Card, error) {
	deletedFilter := "AND p.deleted_at IS NULL"
	if includeDeleted {
		deletedFilter = ""
	}
	row := r.pool.QueryRow(ctx, `
		SELECT
			p.id, p.tree_id, p.sex, p.life_status, p.biography, p.notes,
			p.primary_media_id, p.privacy_level, p.created_by, p.updated_by,
			p.created_at, p.updated_at, p.deleted_at, p.version,
			n.id, n.person_id, n.tree_id, n.type, n.given_name,
			n.patronymic, n.family_name, n.prefix, n.suffix, n.full_text,
			n.is_preferred, n.language_code, n.created_at, n.updated_at
		FROM persons p
		JOIN person_names n
		  ON n.person_id = p.id
		 AND n.tree_id = p.tree_id
		 AND n.is_preferred
		JOIN tree_members m
		  ON m.tree_id = p.tree_id
		 AND m.user_id = $3
		 AND m.status = 'active'
		JOIN family_trees t
		  ON t.id = p.tree_id
		 AND t.deleted_at IS NULL
		WHERE p.tree_id = $1
		  AND p.id = $2
		  `+deletedFilter,
		treeID,
		personID,
		actorUserID,
	)
	card, err := scanCard(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Card{}, domain.ErrPersonNotFound
	}
	if err != nil {
		return domain.Card{}, fmt.Errorf("get accessible person: %w", err)
	}
	return card, nil
}

func (r *Repository) UpdateEditable(
	ctx context.Context,
	actorUserID uuid.UUID,
	card domain.Card,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin person update transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, card.Person.TreeID, actorUserID); err != nil {
		return err
	}
	person := card.Person
	result, err := tx.Exec(ctx, `
		UPDATE persons
		SET sex = $3,
			life_status = $4,
			biography = $5,
			notes = $6,
			updated_by = $7,
			updated_at = $8,
			version = $9
		WHERE tree_id = $1
		  AND id = $2
		  AND deleted_at IS NULL
		  AND version = $9 - 1
	`,
		person.TreeID,
		person.ID,
		person.Sex,
		person.LifeStatus,
		person.Biography,
		person.Notes,
		actorUserID,
		person.UpdatedAt,
		person.Version,
	)
	if err != nil {
		return fmt.Errorf("update person: %w", err)
	}
	if result.RowsAffected() != 1 {
		return domain.ErrPersonVersionConflict
	}
	name := card.PreferredName
	result, err = tx.Exec(ctx, `
		UPDATE person_names
		SET given_name = $4,
			patronymic = $5,
			family_name = $6,
			prefix = $7,
			suffix = $8,
			full_text = $9,
			language_code = $10,
			updated_at = $11
		WHERE tree_id = $1
		  AND person_id = $2
		  AND id = $3
		  AND is_preferred
	`,
		name.TreeID,
		name.PersonID,
		name.ID,
		name.GivenName,
		name.Patronymic,
		name.FamilyName,
		name.Prefix,
		name.Suffix,
		name.FullText,
		name.LanguageCode,
		name.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("update preferred person name: %w", err)
	}
	if result.RowsAffected() != 1 {
		return domain.ErrPersonNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit person update transaction: %w", err)
	}
	return nil
}

func (r *Repository) SoftDeleteEditable(
	ctx context.Context,
	mutation service.AuditMutation,
) error {
	_, err := r.mutateDeletion(ctx, mutation, true)
	return err
}

func (r *Repository) RestoreEditable(
	ctx context.Context,
	mutation service.AuditMutation,
) (domain.Card, error) {
	return r.mutateDeletion(ctx, mutation, false)
}

func (r *Repository) mutateDeletion(
	ctx context.Context,
	mutation service.AuditMutation,
	deletePerson bool,
) (domain.Card, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Card{}, fmt.Errorf("begin person lifecycle transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, mutation.TreeID, mutation.ActorUserID); err != nil {
		return domain.Card{}, err
	}
	deletedAt := any(mutation.OccurredAt)
	deletedPredicate := "deleted_at IS NULL"
	action := "person.deleted"
	if !deletePerson {
		deletedAt = nil
		deletedPredicate = "deleted_at IS NOT NULL"
		action = "person.restored"
	}
	row := tx.QueryRow(ctx, `
		UPDATE persons
		SET deleted_at = $5,
			updated_by = $2,
			updated_at = $6,
			version = version + 1
		WHERE tree_id = $1
		  AND id = $3
		  AND version = $4
		  AND `+deletedPredicate+`
		RETURNING
			id, tree_id, sex, life_status, biography, notes,
			primary_media_id, privacy_level, created_by, updated_by,
			created_at, updated_at, deleted_at, version
	`,
		mutation.TreeID,
		mutation.ActorUserID,
		mutation.PersonID,
		mutation.Version,
		deletedAt,
		mutation.OccurredAt,
	)
	person, err := scanPerson(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Card{}, domain.ErrPersonVersionConflict
	}
	if err != nil {
		return domain.Card{}, fmt.Errorf("mutate person lifecycle: %w", err)
	}
	name, err := scanName(tx.QueryRow(ctx, `
		SELECT
			id, person_id, tree_id, type, given_name, patronymic,
			family_name, prefix, suffix, full_text, is_preferred,
			language_code, created_at, updated_at
		FROM person_names
		WHERE tree_id = $1 AND person_id = $2 AND is_preferred
	`, mutation.TreeID, mutation.PersonID))
	if err != nil {
		return domain.Card{}, fmt.Errorf("load preferred name after lifecycle mutation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (
			id, tree_id, actor_user_id, action, entity_type, entity_id,
			request_id, ip_address, changes, created_at
		) VALUES (
			$1, $2, $3, $4, 'person', $5,
			$6, NULLIF($7, '')::inet,
			jsonb_build_object(
				'previous_version', $8::integer,
				'new_version', $9::integer,
				'deleted_at', $10::timestamptz
			),
			$11
		)
	`,
		mutation.AuditID,
		mutation.TreeID,
		mutation.ActorUserID,
		action,
		mutation.PersonID,
		mutation.RequestID,
		mutation.IPAddress,
		mutation.Version,
		person.Version,
		person.DeletedAt,
		mutation.OccurredAt,
	); err != nil {
		return domain.Card{}, fmt.Errorf("insert person lifecycle audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Card{}, fmt.Errorf("commit person lifecycle transaction: %w", err)
	}
	return domain.Card{Person: person, PreferredName: name}, nil
}

func requireEditableTree(
	ctx context.Context,
	tx pgx.Tx,
	treeID uuid.UUID,
	actorUserID uuid.UUID,
) error {
	var allowed bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM tree_members m
			JOIN family_trees t ON t.id = m.tree_id
			WHERE m.tree_id = $1
			  AND m.user_id = $2
			  AND m.status = 'active'
			  AND m.role IN ('owner', 'editor')
			  AND t.deleted_at IS NULL
		)
	`, treeID, actorUserID).Scan(&allowed)
	if err != nil {
		return fmt.Errorf("authorize editable tree: %w", err)
	}
	if !allowed {
		return domain.ErrPersonAccessDenied
	}
	return nil
}

func insertPreferredName(ctx context.Context, tx pgx.Tx, name domain.PersonName) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO person_names (
			id, person_id, tree_id, type, given_name, patronymic,
			family_name, prefix, suffix, full_text, is_preferred,
			language_code, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12, $13, $13
		)
	`,
		name.ID,
		name.PersonID,
		name.TreeID,
		name.Type,
		name.GivenName,
		name.Patronymic,
		name.FamilyName,
		name.Prefix,
		name.Suffix,
		name.FullText,
		name.IsPreferred,
		name.LanguageCode,
		name.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert preferred person name: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(...any) error
}

func scanCard(row scanner) (domain.Card, error) {
	var card domain.Card
	err := row.Scan(
		&card.Person.ID,
		&card.Person.TreeID,
		&card.Person.Sex,
		&card.Person.LifeStatus,
		&card.Person.Biography,
		&card.Person.Notes,
		&card.Person.PrimaryMediaID,
		&card.Person.PrivacyLevel,
		&card.Person.CreatedBy,
		&card.Person.UpdatedBy,
		&card.Person.CreatedAt,
		&card.Person.UpdatedAt,
		&card.Person.DeletedAt,
		&card.Person.Version,
		&card.PreferredName.ID,
		&card.PreferredName.PersonID,
		&card.PreferredName.TreeID,
		&card.PreferredName.Type,
		&card.PreferredName.GivenName,
		&card.PreferredName.Patronymic,
		&card.PreferredName.FamilyName,
		&card.PreferredName.Prefix,
		&card.PreferredName.Suffix,
		&card.PreferredName.FullText,
		&card.PreferredName.IsPreferred,
		&card.PreferredName.LanguageCode,
		&card.PreferredName.CreatedAt,
		&card.PreferredName.UpdatedAt,
	)
	return card, err
}

func scanPerson(row scanner) (domain.Person, error) {
	var person domain.Person
	err := row.Scan(
		&person.ID,
		&person.TreeID,
		&person.Sex,
		&person.LifeStatus,
		&person.Biography,
		&person.Notes,
		&person.PrimaryMediaID,
		&person.PrivacyLevel,
		&person.CreatedBy,
		&person.UpdatedBy,
		&person.CreatedAt,
		&person.UpdatedAt,
		&person.DeletedAt,
		&person.Version,
	)
	return person, err
}

func scanName(row scanner) (domain.PersonName, error) {
	var name domain.PersonName
	err := row.Scan(
		&name.ID,
		&name.PersonID,
		&name.TreeID,
		&name.Type,
		&name.GivenName,
		&name.Patronymic,
		&name.FamilyName,
		&name.Prefix,
		&name.Suffix,
		&name.FullText,
		&name.IsPreferred,
		&name.LanguageCode,
		&name.CreatedAt,
		&name.UpdatedAt,
	)
	return name, err
}
