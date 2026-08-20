package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ZheglY/family_tree_app/internal/features/unions/domain"
	"github.com/ZheglY/family_tree_app/internal/features/unions/service"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const unionMemberPrimaryKey = "union_members_pkey"

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) CreateWithMembersEditable(
	ctx context.Context,
	actorUserID uuid.UUID,
	aggregate domain.Aggregate,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin family union creation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, aggregate.Union.TreeID, actorUserID); err != nil {
		return err
	}
	personIDs := make([]uuid.UUID, 0, len(aggregate.Members))
	for _, member := range aggregate.Members {
		personIDs = append(personIDs, member.PersonID)
	}
	if err := requireActivePersons(ctx, tx, aggregate.Union.TreeID, personIDs); err != nil {
		return err
	}
	union := aggregate.Union
	if _, err := tx.Exec(ctx, `
		INSERT INTO family_unions (
			id, tree_id, type, end_reason, note, created_by, updated_by,
			created_at, updated_at, deleted_at, version
		) VALUES (
			$1, $2, $3, $4, $5, $6, $6, $7, $7, NULL, $8
		)
	`,
		union.ID,
		union.TreeID,
		union.Type,
		union.EndReason,
		union.Note,
		actorUserID,
		union.CreatedAt,
		union.Version,
	); err != nil {
		return fmt.Errorf("insert family union: %w", err)
	}
	for _, member := range aggregate.Members {
		if err := insertMember(ctx, tx, member); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit family union creation transaction: %w", err)
	}
	return nil
}

func (r *Repository) GetAccessible(
	ctx context.Context,
	treeID uuid.UUID,
	unionID uuid.UUID,
	actorUserID uuid.UUID,
) (domain.Aggregate, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			u.id, u.tree_id, u.type, u.end_reason, u.note,
			u.created_by, u.updated_by, u.created_at, u.updated_at,
			u.deleted_at, u.version
		FROM family_unions u
		JOIN tree_members m
		  ON m.tree_id = u.tree_id
		 AND m.user_id = $3
		 AND m.status = 'active'
		JOIN family_trees t
		  ON t.id = u.tree_id
		 AND t.deleted_at IS NULL
		WHERE u.tree_id = $1
		  AND u.id = $2
		  AND u.deleted_at IS NULL
	`, treeID, unionID, actorUserID)
	union, err := scanUnion(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Aggregate{}, domain.ErrUnionNotFound
	}
	if err != nil {
		return domain.Aggregate{}, fmt.Errorf("get accessible family union: %w", err)
	}
	members, err := loadMembers(ctx, r.pool, treeID, unionID)
	if err != nil {
		return domain.Aggregate{}, err
	}
	return domain.Aggregate{Union: union, Members: members}, nil
}

func (r *Repository) UpdateEditable(
	ctx context.Context,
	actorUserID uuid.UUID,
	union domain.FamilyUnion,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin family union update transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, union.TreeID, actorUserID); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `
		UPDATE family_unions
		SET type = $3,
			end_reason = $4,
			note = $5,
			updated_by = $6,
			updated_at = $7,
			version = $8
		WHERE tree_id = $1
		  AND id = $2
		  AND deleted_at IS NULL
		  AND version = $8 - 1
	`,
		union.TreeID,
		union.ID,
		union.Type,
		union.EndReason,
		union.Note,
		actorUserID,
		union.UpdatedAt,
		union.Version,
	)
	if err != nil {
		return fmt.Errorf("update family union: %w", err)
	}
	if result.RowsAffected() != 1 {
		return domain.ErrUnionVersionConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit family union update transaction: %w", err)
	}
	return nil
}

func (r *Repository) SoftDeleteEditable(
	ctx context.Context,
	mutation service.AuditMutation,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin family union deletion transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, mutation.TreeID, mutation.ActorUserID); err != nil {
		return err
	}
	var newVersion int
	err = tx.QueryRow(ctx, `
		UPDATE family_unions
		SET deleted_at = $5,
			updated_by = $2,
			updated_at = $5,
			version = version + 1
		WHERE tree_id = $1
		  AND id = $3
		  AND version = $4
		  AND deleted_at IS NULL
		RETURNING version
	`,
		mutation.TreeID,
		mutation.ActorUserID,
		mutation.UnionID,
		mutation.Version,
		mutation.OccurredAt,
	).Scan(&newVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrUnionVersionConflict
	}
	if err != nil {
		return fmt.Errorf("soft delete family union: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (
			id, tree_id, actor_user_id, action, entity_type, entity_id,
			request_id, ip_address, changes, created_at
		) VALUES (
			$1, $2, $3, 'family_union.deleted', 'family_union', $4,
			$5, NULLIF($6, '')::inet,
			jsonb_build_object(
				'previous_version', $7::integer,
				'new_version', $8::integer,
				'deleted_at', $9::timestamptz
			),
			$9
		)
	`,
		mutation.AuditID,
		mutation.TreeID,
		mutation.ActorUserID,
		mutation.UnionID,
		mutation.RequestID,
		mutation.IPAddress,
		mutation.Version,
		newVersion,
		mutation.OccurredAt,
	); err != nil {
		return fmt.Errorf("insert family union deletion audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit family union deletion transaction: %w", err)
	}
	return nil
}

func (r *Repository) AddMemberEditable(
	ctx context.Context,
	actorUserID uuid.UUID,
	member domain.UnionMember,
) (domain.Aggregate, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Aggregate{}, fmt.Errorf("begin union member addition transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, member.TreeID, actorUserID); err != nil {
		return domain.Aggregate{}, err
	}
	union, err := loadUnionForUpdate(ctx, tx, member.TreeID, member.UnionID)
	if err != nil {
		return domain.Aggregate{}, err
	}
	if err := requireActivePersons(ctx, tx, member.TreeID, []uuid.UUID{member.PersonID}); err != nil {
		return domain.Aggregate{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM union_members WHERE tree_id = $1 AND union_id = $2
	`, member.TreeID, member.UnionID).Scan(&count); err != nil {
		return domain.Aggregate{}, fmt.Errorf("count union members: %w", err)
	}
	if count >= domain.MaxMembers {
		return domain.Aggregate{}, domain.ErrUnionMemberLimit
	}
	if err := insertMember(ctx, tx, member); err != nil {
		return domain.Aggregate{}, err
	}
	union, err = bumpUnionVersion(ctx, tx, union, actorUserID, member.CreatedAt)
	if err != nil {
		return domain.Aggregate{}, err
	}
	members, err := loadMembers(ctx, tx, member.TreeID, member.UnionID)
	if err != nil {
		return domain.Aggregate{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Aggregate{}, fmt.Errorf("commit union member addition transaction: %w", err)
	}
	return domain.Aggregate{Union: union, Members: members}, nil
}

func (r *Repository) RemoveMemberEditable(
	ctx context.Context,
	mutation service.MemberMutation,
) (domain.Aggregate, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Aggregate{}, fmt.Errorf("begin union member removal transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, mutation.TreeID, mutation.ActorUserID); err != nil {
		return domain.Aggregate{}, err
	}
	union, err := loadUnionForUpdate(ctx, tx, mutation.TreeID, mutation.UnionID)
	if err != nil {
		return domain.Aggregate{}, err
	}
	var memberExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM union_members
			WHERE tree_id = $1 AND union_id = $2 AND person_id = $3
		)
	`, mutation.TreeID, mutation.UnionID, mutation.PersonID).Scan(&memberExists); err != nil {
		return domain.Aggregate{}, fmt.Errorf("check union member existence: %w", err)
	}
	if !memberExists {
		return domain.Aggregate{}, domain.ErrUnionMemberNotFound
	}
	var count int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM union_members WHERE tree_id = $1 AND union_id = $2
	`, mutation.TreeID, mutation.UnionID).Scan(&count); err != nil {
		return domain.Aggregate{}, fmt.Errorf("count union members: %w", err)
	}
	if count <= domain.MinMembers {
		return domain.Aggregate{}, domain.ErrUnionMemberLimit
	}
	result, err := tx.Exec(ctx, `
		DELETE FROM union_members
		WHERE tree_id = $1 AND union_id = $2 AND person_id = $3
	`, mutation.TreeID, mutation.UnionID, mutation.PersonID)
	if err != nil {
		return domain.Aggregate{}, fmt.Errorf("remove union member: %w", err)
	}
	if result.RowsAffected() != 1 {
		return domain.Aggregate{}, domain.ErrUnionMemberNotFound
	}
	union, err = bumpUnionVersion(ctx, tx, union, mutation.ActorUserID, mutation.OccurredAt)
	if err != nil {
		return domain.Aggregate{}, err
	}
	members, err := loadMembers(ctx, tx, mutation.TreeID, mutation.UnionID)
	if err != nil {
		return domain.Aggregate{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Aggregate{}, fmt.Errorf("commit union member removal transaction: %w", err)
	}
	return domain.Aggregate{Union: union, Members: members}, nil
}

func loadUnionForUpdate(
	ctx context.Context,
	tx pgx.Tx,
	treeID uuid.UUID,
	unionID uuid.UUID,
) (domain.FamilyUnion, error) {
	union, err := scanUnion(tx.QueryRow(ctx, `
		SELECT
			id, tree_id, type, end_reason, note,
			created_by, updated_by, created_at, updated_at,
			deleted_at, version
		FROM family_unions
		WHERE tree_id = $1 AND id = $2 AND deleted_at IS NULL
		FOR UPDATE
	`, treeID, unionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.FamilyUnion{}, domain.ErrUnionNotFound
	}
	if err != nil {
		return domain.FamilyUnion{}, fmt.Errorf("lock family union: %w", err)
	}
	return union, nil
}

func bumpUnionVersion(
	ctx context.Context,
	tx pgx.Tx,
	union domain.FamilyUnion,
	actorUserID uuid.UUID,
	now time.Time,
) (domain.FamilyUnion, error) {
	err := tx.QueryRow(ctx, `
		UPDATE family_unions
		SET updated_by = $3,
			updated_at = $4,
			version = version + 1
		WHERE tree_id = $1 AND id = $2 AND deleted_at IS NULL
		RETURNING
			id, tree_id, type, end_reason, note,
			created_by, updated_by, created_at, updated_at,
			deleted_at, version
	`, union.TreeID, union.ID, actorUserID, now).Scan(
		&union.ID,
		&union.TreeID,
		&union.Type,
		&union.EndReason,
		&union.Note,
		&union.CreatedBy,
		&union.UpdatedBy,
		&union.CreatedAt,
		&union.UpdatedAt,
		&union.DeletedAt,
		&union.Version,
	)
	if err != nil {
		return domain.FamilyUnion{}, fmt.Errorf("increment family union version: %w", err)
	}
	return union, nil
}

func insertMember(ctx context.Context, tx pgx.Tx, member domain.UnionMember) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO union_members (
			union_id, person_id, tree_id, role, created_at
		) VALUES ($1, $2, $3, $4, $5)
	`, member.UnionID, member.PersonID, member.TreeID, member.Role, member.CreatedAt)
	if err == nil {
		return nil
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" &&
		postgresError.ConstraintName == unionMemberPrimaryKey {
		return domain.ErrDuplicateUnionMember
	}
	return fmt.Errorf("insert union member: %w", err)
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func loadMembers(
	ctx context.Context,
	queries queryer,
	treeID uuid.UUID,
	unionID uuid.UUID,
) ([]domain.UnionMember, error) {
	rows, err := queries.Query(ctx, `
		SELECT union_id, person_id, tree_id, role, created_at
		FROM union_members
		WHERE tree_id = $1 AND union_id = $2
		ORDER BY created_at, person_id
	`, treeID, unionID)
	if err != nil {
		return nil, fmt.Errorf("load union members: %w", err)
	}
	defer rows.Close()
	members := make([]domain.UnionMember, 0)
	for rows.Next() {
		var member domain.UnionMember
		if err := rows.Scan(
			&member.UnionID,
			&member.PersonID,
			&member.TreeID,
			&member.Role,
			&member.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan union member: %w", err)
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate union members: %w", err)
	}
	return members, nil
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
		return fmt.Errorf("authorize editable union tree: %w", err)
	}
	if !allowed {
		return domain.ErrUnionAccessDenied
	}
	return nil
}

func requireActivePersons(
	ctx context.Context,
	tx pgx.Tx,
	treeID uuid.UUID,
	personIDs []uuid.UUID,
) error {
	var count int
	err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM persons
		WHERE tree_id = $1
		  AND id = ANY($2::uuid[])
		  AND deleted_at IS NULL
	`, treeID, personIDs).Scan(&count)
	if err != nil {
		return fmt.Errorf("validate union persons: %w", err)
	}
	if count != len(personIDs) {
		return domain.ErrUnionNotFound
	}
	return nil
}

type scanner interface {
	Scan(...any) error
}

func scanUnion(row scanner) (domain.FamilyUnion, error) {
	var union domain.FamilyUnion
	err := row.Scan(
		&union.ID,
		&union.TreeID,
		&union.Type,
		&union.EndReason,
		&union.Note,
		&union.CreatedBy,
		&union.UpdatedBy,
		&union.CreatedAt,
		&union.UpdatedAt,
		&union.DeletedAt,
		&union.Version,
	)
	return union, err
}
