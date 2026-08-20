package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	"github.com/ZheglY/family_tree_app/internal/features/trees/service"
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

func (r *Repository) CreateWithOwner(
	ctx context.Context,
	access domain.TreeAccess,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tree creation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tree := access.Tree
	if _, err := tx.Exec(ctx, `
		INSERT INTO family_trees (
			id, name, description, owner_user_id, root_person_id,
			cover_media_id, privacy, locale, timezone,
			created_at, updated_at, deleted_at, version
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10, NULL, $11)
	`,
		tree.ID,
		tree.Name,
		tree.Description,
		tree.OwnerUserID,
		tree.RootPersonID,
		tree.CoverMediaID,
		tree.Privacy,
		tree.Locale,
		tree.Timezone,
		tree.CreatedAt,
		tree.Version,
	); err != nil {
		return fmt.Errorf("insert family tree: %w", err)
	}
	member := access.Membership
	if _, err := tx.Exec(ctx, `
		INSERT INTO tree_members (
			tree_id, user_id, role, status, invited_by, created_at, accepted_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`,
		member.TreeID,
		member.UserID,
		member.Role,
		member.Status,
		member.InvitedBy,
		member.CreatedAt,
		member.AcceptedAt,
	); err != nil {
		return fmt.Errorf("insert tree owner membership: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tree creation transaction: %w", err)
	}
	return nil
}

func (r *Repository) ListAccessible(
	ctx context.Context,
	userID uuid.UUID,
) ([]domain.TreeAccess, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			t.id, t.name, t.description, t.owner_user_id,
			t.root_person_id, t.cover_media_id, t.privacy, t.locale, t.timezone,
			t.created_at, t.updated_at, t.deleted_at, t.version,
			m.tree_id, m.user_id, m.role, m.status, m.invited_by,
			m.created_at, m.accepted_at
		FROM family_trees t
		JOIN tree_members m ON m.tree_id = t.id
		WHERE m.user_id = $1
		  AND m.status = 'active'
		  AND t.deleted_at IS NULL
		ORDER BY t.updated_at DESC, t.id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list accessible family trees: %w", err)
	}
	defer rows.Close()

	items := make([]domain.TreeAccess, 0)
	for rows.Next() {
		access, err := scanTreeAccess(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, access)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accessible family trees: %w", err)
	}
	return items, nil
}

func (r *Repository) GetAccessible(
	ctx context.Context,
	treeID uuid.UUID,
	userID uuid.UUID,
	includeDeleted bool,
) (domain.TreeAccess, error) {
	deletedFilter := "AND t.deleted_at IS NULL"
	if includeDeleted {
		deletedFilter = ""
	}
	row := r.pool.QueryRow(ctx, `
		SELECT
			t.id, t.name, t.description, t.owner_user_id,
			t.root_person_id, t.cover_media_id, t.privacy, t.locale, t.timezone,
			t.created_at, t.updated_at, t.deleted_at, t.version,
			m.tree_id, m.user_id, m.role, m.status, m.invited_by,
			m.created_at, m.accepted_at
		FROM family_trees t
		JOIN tree_members m ON m.tree_id = t.id
		WHERE t.id = $1
		  AND m.user_id = $2
		  AND m.status = 'active'
		`+deletedFilter,
		treeID,
		userID,
	)
	access, err := scanTreeAccess(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.TreeAccess{}, domain.ErrTreeNotFound
	}
	return access, err
}

func (r *Repository) UpdateOwned(
	ctx context.Context,
	actorUserID uuid.UUID,
	tree domain.FamilyTree,
) (domain.FamilyTree, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE family_trees t
		SET name = $3,
			description = $4,
			locale = $5,
			timezone = $6,
			updated_at = $7,
			version = $8
		WHERE t.id = $1
		  AND t.owner_user_id = $2
		  AND t.deleted_at IS NULL
		  AND t.version = $8 - 1
		  AND EXISTS (
			SELECT 1
			FROM tree_members m
			WHERE m.tree_id = t.id
			  AND m.user_id = $2
			  AND m.role = 'owner'
			  AND m.status = 'active'
		  )
		RETURNING
			t.id, t.name, t.description, t.owner_user_id,
			t.root_person_id, t.cover_media_id, t.privacy, t.locale, t.timezone,
			t.created_at, t.updated_at, t.deleted_at, t.version
	`,
		tree.ID,
		actorUserID,
		tree.Name,
		tree.Description,
		tree.Locale,
		tree.Timezone,
		tree.UpdatedAt,
		tree.Version,
	)
	updated, err := scanTree(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.FamilyTree{}, domain.ErrTreeVersionConflict
	}
	return updated, err
}

func (r *Repository) SoftDeleteOwned(
	ctx context.Context,
	mutation service.AuditMutation,
) error {
	_, err := r.mutateDeletion(ctx, mutation, true)
	return err
}

func (r *Repository) RestoreOwned(
	ctx context.Context,
	mutation service.AuditMutation,
) (domain.FamilyTree, error) {
	return r.mutateDeletion(ctx, mutation, false)
}

func (r *Repository) mutateDeletion(
	ctx context.Context,
	mutation service.AuditMutation,
	deleteTree bool,
) (domain.FamilyTree, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.FamilyTree{}, fmt.Errorf("begin tree lifecycle transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	deletedAt := any(mutation.OccurredAt)
	deletedPredicate := "t.deleted_at IS NULL"
	action := "tree.deleted"
	if !deleteTree {
		deletedAt = nil
		deletedPredicate = "t.deleted_at IS NOT NULL"
		action = "tree.restored"
	}
	row := tx.QueryRow(ctx, `
		UPDATE family_trees t
		SET deleted_at = $4,
			updated_at = $5,
			version = version + 1
		WHERE t.id = $1
		  AND t.owner_user_id = $2
		  AND t.version = $3
		  AND `+deletedPredicate+`
		  AND EXISTS (
			SELECT 1
			FROM tree_members m
			WHERE m.tree_id = t.id
			  AND m.user_id = $2
			  AND m.role = 'owner'
			  AND m.status = 'active'
		  )
		RETURNING
			t.id, t.name, t.description, t.owner_user_id,
			t.root_person_id, t.cover_media_id, t.privacy, t.locale, t.timezone,
			t.created_at, t.updated_at, t.deleted_at, t.version
	`,
		mutation.TreeID,
		mutation.ActorUserID,
		mutation.Version,
		deletedAt,
		mutation.OccurredAt,
	)
	updated, err := scanTree(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.FamilyTree{}, domain.ErrTreeVersionConflict
	}
	if err != nil {
		return domain.FamilyTree{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (
			id, tree_id, actor_user_id, action, entity_type, entity_id,
			request_id, ip_address, changes, created_at
		) VALUES (
			$1, $2, $3, $4, 'family_tree', $2,
			$5, NULLIF($6, '')::inet,
			jsonb_build_object(
				'previous_version', $7::integer,
				'new_version', $8::integer,
				'deleted_at', $9::timestamptz
			),
			$10
		)
	`,
		mutation.AuditID,
		mutation.TreeID,
		mutation.ActorUserID,
		action,
		mutation.RequestID,
		mutation.IPAddress,
		mutation.Version,
		updated.Version,
		updated.DeletedAt,
		mutation.OccurredAt,
	); err != nil {
		return domain.FamilyTree{}, fmt.Errorf("insert tree lifecycle audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.FamilyTree{}, fmt.Errorf("commit tree lifecycle transaction: %w", err)
	}
	return updated, nil
}

type scanner interface {
	Scan(...any) error
}

func scanTreeAccess(row scanner) (domain.TreeAccess, error) {
	var access domain.TreeAccess
	if err := row.Scan(
		&access.Tree.ID,
		&access.Tree.Name,
		&access.Tree.Description,
		&access.Tree.OwnerUserID,
		&access.Tree.RootPersonID,
		&access.Tree.CoverMediaID,
		&access.Tree.Privacy,
		&access.Tree.Locale,
		&access.Tree.Timezone,
		&access.Tree.CreatedAt,
		&access.Tree.UpdatedAt,
		&access.Tree.DeletedAt,
		&access.Tree.Version,
		&access.Membership.TreeID,
		&access.Membership.UserID,
		&access.Membership.Role,
		&access.Membership.Status,
		&access.Membership.InvitedBy,
		&access.Membership.CreatedAt,
		&access.Membership.AcceptedAt,
	); err != nil {
		return domain.TreeAccess{}, err
	}
	return access, nil
}

func scanTree(row scanner) (domain.FamilyTree, error) {
	var tree domain.FamilyTree
	if err := row.Scan(
		&tree.ID,
		&tree.Name,
		&tree.Description,
		&tree.OwnerUserID,
		&tree.RootPersonID,
		&tree.CoverMediaID,
		&tree.Privacy,
		&tree.Locale,
		&tree.Timezone,
		&tree.CreatedAt,
		&tree.UpdatedAt,
		&tree.DeletedAt,
		&tree.Version,
	); err != nil {
		return domain.FamilyTree{}, err
	}
	return tree, nil
}
