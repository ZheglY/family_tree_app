package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/ZheglY/family_tree_app/internal/features/relationships/domain"
	"github.com/ZheglY/family_tree_app/internal/features/relationships/service"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const relationUniqueConstraint = "parent_child_relations_active_uq"

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) CreateAcyclicEditable(
	ctx context.Context,
	actorUserID uuid.UUID,
	relation domain.ParentChildRelation,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin relation creation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, relation.TreeID, actorUserID); err != nil {
		return err
	}
	if err := lockTreeGraph(ctx, tx, relation.TreeID); err != nil {
		return err
	}
	if err := requireActivePersons(
		ctx,
		tx,
		relation.TreeID,
		relation.ParentPersonID,
		relation.ChildPersonID,
	); err != nil {
		return err
	}
	var duplicate bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM parent_child_relations
			WHERE tree_id = $1
			  AND parent_person_id = $2
			  AND child_person_id = $3
			  AND relation_type = $4
			  AND deleted_at IS NULL
		)
	`,
		relation.TreeID,
		relation.ParentPersonID,
		relation.ChildPersonID,
		relation.RelationType,
	).Scan(&duplicate); err != nil {
		return fmt.Errorf("check duplicate parent-child relation: %w", err)
	}
	if duplicate {
		return domain.ErrDuplicateRelation
	}
	var createsCycle bool
	if err := tx.QueryRow(ctx, `
		WITH RECURSIVE descendants (person_id, path) AS (
			SELECT $2::uuid, ARRAY[$2::uuid]
			UNION
			SELECT r.child_person_id, d.path || r.child_person_id
			FROM descendants d
			JOIN parent_child_relations r
			  ON r.tree_id = $1
			 AND r.parent_person_id = d.person_id
			 AND r.deleted_at IS NULL
			JOIN persons child
			  ON child.tree_id = r.tree_id
			 AND child.id = r.child_person_id
			 AND child.deleted_at IS NULL
			WHERE NOT r.child_person_id = ANY(d.path)
		)
		SELECT EXISTS (
			SELECT 1 FROM descendants WHERE person_id = $3
		)
	`,
		relation.TreeID,
		relation.ChildPersonID,
		relation.ParentPersonID,
	).Scan(&createsCycle); err != nil {
		return fmt.Errorf("check parent-child relation cycle: %w", err)
	}
	if createsCycle {
		return domain.ErrRelationCycle
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO parent_child_relations (
			id, tree_id, parent_person_id, child_person_id,
			relation_type, confidence, note, created_by,
			created_at, updated_at, deleted_at, version
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $9, NULL, $10
		)
	`,
		relation.ID,
		relation.TreeID,
		relation.ParentPersonID,
		relation.ChildPersonID,
		relation.RelationType,
		relation.Confidence,
		relation.Note,
		actorUserID,
		relation.CreatedAt,
		relation.Version,
	)
	if err != nil {
		return mapWriteError("insert parent-child relation", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit relation creation transaction: %w", err)
	}
	return nil
}

func (r *Repository) GetAccessible(
	ctx context.Context,
	treeID uuid.UUID,
	relationID uuid.UUID,
	actorUserID uuid.UUID,
) (domain.ParentChildRelation, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			r.id, r.tree_id, r.parent_person_id, r.child_person_id,
			r.relation_type, r.confidence, r.note, r.created_by,
			r.created_at, r.updated_at, r.deleted_at, r.version
		FROM parent_child_relations r
		JOIN tree_members m
		  ON m.tree_id = r.tree_id
		 AND m.user_id = $3
		 AND m.status = 'active'
		JOIN family_trees t
		  ON t.id = r.tree_id
		 AND t.deleted_at IS NULL
		JOIN persons parent
		  ON parent.tree_id = r.tree_id
		 AND parent.id = r.parent_person_id
		 AND parent.deleted_at IS NULL
		JOIN persons child
		  ON child.tree_id = r.tree_id
		 AND child.id = r.child_person_id
		 AND child.deleted_at IS NULL
		WHERE r.tree_id = $1
		  AND r.id = $2
		  AND r.deleted_at IS NULL
	`, treeID, relationID, actorUserID)
	relation, err := scanRelation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ParentChildRelation{}, domain.ErrRelationNotFound
	}
	if err != nil {
		return domain.ParentChildRelation{}, fmt.Errorf("get accessible parent-child relation: %w", err)
	}
	return relation, nil
}

func (r *Repository) UpdateEditable(
	ctx context.Context,
	actorUserID uuid.UUID,
	relation domain.ParentChildRelation,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin relation update transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, relation.TreeID, actorUserID); err != nil {
		return err
	}
	if err := lockTreeGraph(ctx, tx, relation.TreeID); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `
		UPDATE parent_child_relations r
		SET relation_type = $3,
			confidence = $4,
			note = $5,
			updated_at = $6,
			version = $7
		WHERE r.tree_id = $1
		  AND r.id = $2
		  AND r.deleted_at IS NULL
		  AND r.version = $7 - 1
		  AND EXISTS (
			SELECT 1 FROM persons p
			WHERE p.tree_id = r.tree_id
			  AND p.id = r.parent_person_id
			  AND p.deleted_at IS NULL
		  )
		  AND EXISTS (
			SELECT 1 FROM persons p
			WHERE p.tree_id = r.tree_id
			  AND p.id = r.child_person_id
			  AND p.deleted_at IS NULL
		  )
	`,
		relation.TreeID,
		relation.ID,
		relation.RelationType,
		relation.Confidence,
		relation.Note,
		relation.UpdatedAt,
		relation.Version,
	)
	if err != nil {
		return mapWriteError("update parent-child relation", err)
	}
	if result.RowsAffected() != 1 {
		return domain.ErrRelationVersionConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit relation update transaction: %w", err)
	}
	return nil
}

func (r *Repository) SoftDeleteEditable(
	ctx context.Context,
	mutation service.AuditMutation,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin relation deletion transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, mutation.TreeID, mutation.ActorUserID); err != nil {
		return err
	}
	if err := lockTreeGraph(ctx, tx, mutation.TreeID); err != nil {
		return err
	}
	var newVersion int
	err = tx.QueryRow(ctx, `
		UPDATE parent_child_relations r
		SET deleted_at = $4,
			updated_at = $4,
			version = version + 1
		WHERE r.tree_id = $1
		  AND r.id = $2
		  AND r.version = $3
		  AND r.deleted_at IS NULL
		RETURNING r.version
	`,
		mutation.TreeID,
		mutation.RelationID,
		mutation.Version,
		mutation.OccurredAt,
	).Scan(&newVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrRelationVersionConflict
	}
	if err != nil {
		return fmt.Errorf("soft delete parent-child relation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (
			id, tree_id, actor_user_id, action, entity_type, entity_id,
			request_id, ip_address, changes, created_at
		) VALUES (
			$1, $2, $3, 'parent_child_relation.deleted',
			'parent_child_relation', $4,
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
		mutation.RelationID,
		mutation.RequestID,
		mutation.IPAddress,
		mutation.Version,
		newVersion,
		mutation.OccurredAt,
	); err != nil {
		return fmt.Errorf("insert relation deletion audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit relation deletion transaction: %w", err)
	}
	return nil
}

func (r *Repository) LoadGraphAccessible(
	ctx context.Context,
	filter service.GraphFilter,
) (domain.Graph, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return domain.Graph{}, fmt.Errorf("begin graph read transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var centerAccessible bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM persons p
			JOIN tree_members m
			  ON m.tree_id = p.tree_id
			 AND m.user_id = $3
			 AND m.status = 'active'
			JOIN family_trees t
			  ON t.id = p.tree_id
			 AND t.deleted_at IS NULL
			WHERE p.tree_id = $1
			  AND p.id = $2
			  AND p.deleted_at IS NULL
		)
	`, filter.TreeID, filter.CenterPersonID, filter.ActorUserID).Scan(&centerAccessible)
	if err != nil {
		return domain.Graph{}, fmt.Errorf("authorize graph center: %w", err)
	}
	if !centerAccessible {
		return domain.Graph{}, domain.ErrRelationNotFound
	}
	rows, err := tx.Query(ctx, `
		WITH RECURSIVE
		ancestors (person_id, depth, path) AS (
			SELECT $2::uuid, 0, ARRAY[$2::uuid]
			UNION
			SELECT r.parent_person_id, a.depth + 1, a.path || r.parent_person_id
			FROM ancestors a
			JOIN parent_child_relations r
			  ON r.tree_id = $1
			 AND r.child_person_id = a.person_id
			 AND r.deleted_at IS NULL
			JOIN persons parent
			  ON parent.tree_id = r.tree_id
			 AND parent.id = r.parent_person_id
			 AND parent.deleted_at IS NULL
			WHERE a.depth < $3
			  AND NOT r.parent_person_id = ANY(a.path)
		),
		descendants (person_id, depth, path) AS (
			SELECT $2::uuid, 0, ARRAY[$2::uuid]
			UNION
			SELECT r.child_person_id, d.depth + 1, d.path || r.child_person_id
			FROM descendants d
			JOIN parent_child_relations r
			  ON r.tree_id = $1
			 AND r.parent_person_id = d.person_id
			 AND r.deleted_at IS NULL
			JOIN persons child
			  ON child.tree_id = r.tree_id
			 AND child.id = r.child_person_id
			 AND child.deleted_at IS NULL
			WHERE d.depth < $4
			  AND NOT r.child_person_id = ANY(d.path)
		),
		nodes AS (
			SELECT person_id FROM ancestors
			UNION
			SELECT person_id FROM descendants
		)
		SELECT person_id
		FROM nodes
		LIMIT $5
	`,
		filter.TreeID,
		filter.CenterPersonID,
		filter.AncestorsDepth,
		filter.DescendantsDepth,
		filter.MaxNodes+1,
	)
	if err != nil {
		return domain.Graph{}, fmt.Errorf("load graph node identifiers: %w", err)
	}
	var nodeIDs []uuid.UUID
	for rows.Next() {
		var personID uuid.UUID
		if err := rows.Scan(&personID); err != nil {
			rows.Close()
			return domain.Graph{}, fmt.Errorf("scan graph node identifier: %w", err)
		}
		nodeIDs = append(nodeIDs, personID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.Graph{}, fmt.Errorf("iterate graph node identifiers: %w", err)
	}
	rows.Close()
	if len(nodeIDs) > filter.MaxNodes {
		return domain.Graph{}, domain.ErrGraphLimitExceeded
	}
	lineageNodeIDs := append([]uuid.UUID(nil), nodeIDs...)
	var unionIDs []uuid.UUID
	if filter.IncludePartners {
		partnerIDs, loadedUnionIDs, err := loadPartnerGraphNodes(
			ctx,
			tx,
			filter.TreeID,
			lineageNodeIDs,
		)
		if err != nil {
			return domain.Graph{}, err
		}
		nodeIDs = appendUniqueUUIDs(nodeIDs, partnerIDs...)
		unionIDs = loadedUnionIDs
		if len(nodeIDs) > filter.MaxNodes {
			return domain.Graph{}, domain.ErrGraphLimitExceeded
		}
	}
	persons, err := loadPersonSummaries(ctx, tx, filter.TreeID, nodeIDs)
	if err != nil {
		return domain.Graph{}, err
	}
	unions, unionMembers, err := loadGraphUnions(
		ctx,
		tx,
		filter.TreeID,
		unionIDs,
		nodeIDs,
	)
	if err != nil {
		return domain.Graph{}, err
	}
	relations, err := loadRelationsForNodes(ctx, tx, filter.TreeID, nodeIDs)
	if err != nil {
		return domain.Graph{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Graph{}, fmt.Errorf("commit graph read transaction: %w", err)
	}
	return domain.Graph{
		CenterPersonID: filter.CenterPersonID,
		Persons:        persons,
		Relations:      relations,
		Unions:         unions,
		UnionMembers:   unionMembers,
	}, nil
}

func loadPartnerGraphNodes(
	ctx context.Context,
	tx pgx.Tx,
	treeID uuid.UUID,
	lineageNodeIDs []uuid.UUID,
) ([]uuid.UUID, []uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT u.id, member.person_id
		FROM family_unions u
		JOIN union_members anchor
		  ON anchor.tree_id = u.tree_id
		 AND anchor.union_id = u.id
		JOIN union_members member
		  ON member.tree_id = u.tree_id
		 AND member.union_id = u.id
		JOIN persons person
		  ON person.tree_id = member.tree_id
		 AND person.id = member.person_id
		 AND person.deleted_at IS NULL
		WHERE u.tree_id = $1
		  AND u.deleted_at IS NULL
		  AND anchor.person_id = ANY($2::uuid[])
		  AND (
			SELECT count(*)
			FROM union_members active_member
			JOIN persons active_person
			  ON active_person.tree_id = active_member.tree_id
			 AND active_person.id = active_member.person_id
			 AND active_person.deleted_at IS NULL
			WHERE active_member.tree_id = u.tree_id
			  AND active_member.union_id = u.id
		  ) >= 2
		ORDER BY u.id, member.person_id
	`, treeID, lineageNodeIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("load graph partner nodes: %w", err)
	}
	defer rows.Close()
	var personIDs []uuid.UUID
	var unionIDs []uuid.UUID
	for rows.Next() {
		var unionID, personID uuid.UUID
		if err := rows.Scan(&unionID, &personID); err != nil {
			return nil, nil, fmt.Errorf("scan graph partner node: %w", err)
		}
		personIDs = appendUniqueUUIDs(personIDs, personID)
		unionIDs = appendUniqueUUIDs(unionIDs, unionID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate graph partner nodes: %w", err)
	}
	return personIDs, unionIDs, nil
}

func loadGraphUnions(
	ctx context.Context,
	tx pgx.Tx,
	treeID uuid.UUID,
	unionIDs []uuid.UUID,
	nodeIDs []uuid.UUID,
) ([]domain.FamilyUnionSummary, []domain.UnionMemberSummary, error) {
	if len(unionIDs) == 0 {
		return []domain.FamilyUnionSummary{}, []domain.UnionMemberSummary{}, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT id, tree_id, type, end_reason, note, version
		FROM family_unions
		WHERE tree_id = $1
		  AND id = ANY($2::uuid[])
		  AND deleted_at IS NULL
		ORDER BY created_at, id
	`, treeID, unionIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("load graph unions: %w", err)
	}
	var unions []domain.FamilyUnionSummary
	for rows.Next() {
		var union domain.FamilyUnionSummary
		if err := rows.Scan(
			&union.ID,
			&union.TreeID,
			&union.Type,
			&union.EndReason,
			&union.Note,
			&union.Version,
		); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("scan graph union: %w", err)
		}
		unions = append(unions, union)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, fmt.Errorf("iterate graph unions: %w", err)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `
		SELECT union_id, person_id, role
		FROM union_members
		WHERE tree_id = $1
		  AND union_id = ANY($2::uuid[])
		  AND person_id = ANY($3::uuid[])
		ORDER BY union_id, created_at, person_id
	`, treeID, unionIDs, nodeIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("load graph union members: %w", err)
	}
	defer rows.Close()
	var members []domain.UnionMemberSummary
	for rows.Next() {
		var member domain.UnionMemberSummary
		if err := rows.Scan(&member.UnionID, &member.PersonID, &member.Role); err != nil {
			return nil, nil, fmt.Errorf("scan graph union member: %w", err)
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate graph union members: %w", err)
	}
	return unions, members, nil
}

func appendUniqueUUIDs(destination []uuid.UUID, values ...uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(destination)+len(values))
	for _, value := range destination {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		destination = append(destination, value)
	}
	return destination
}

func loadPersonSummaries(
	ctx context.Context,
	tx pgx.Tx,
	treeID uuid.UUID,
	nodeIDs []uuid.UUID,
) ([]domain.PersonSummary, error) {
	rows, err := tx.Query(ctx, `
		SELECT
			p.id, p.sex, p.life_status, p.primary_media_id,
			n.id, n.given_name, n.patronymic, n.family_name,
			n.prefix, n.suffix, n.full_text, n.language_code
		FROM persons p
		JOIN person_names n
		  ON n.tree_id = p.tree_id
		 AND n.person_id = p.id
		 AND n.is_preferred
		WHERE p.tree_id = $1
		  AND p.id = ANY($2::uuid[])
		  AND p.deleted_at IS NULL
		ORDER BY n.full_text, p.id
	`, treeID, nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("load graph person summaries: %w", err)
	}
	defer rows.Close()
	persons := make([]domain.PersonSummary, 0, len(nodeIDs))
	for rows.Next() {
		var person domain.PersonSummary
		if err := rows.Scan(
			&person.ID,
			&person.Sex,
			&person.LifeStatus,
			&person.PrimaryMediaID,
			&person.PreferredName.ID,
			&person.PreferredName.GivenName,
			&person.PreferredName.Patronymic,
			&person.PreferredName.FamilyName,
			&person.PreferredName.Prefix,
			&person.PreferredName.Suffix,
			&person.PreferredName.FullText,
			&person.PreferredName.LanguageCode,
		); err != nil {
			return nil, fmt.Errorf("scan graph person summary: %w", err)
		}
		persons = append(persons, person)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate graph person summaries: %w", err)
	}
	return persons, nil
}

func loadRelationsForNodes(
	ctx context.Context,
	tx pgx.Tx,
	treeID uuid.UUID,
	nodeIDs []uuid.UUID,
) ([]domain.ParentChildRelation, error) {
	rows, err := tx.Query(ctx, `
		SELECT
			r.id, r.tree_id, r.parent_person_id, r.child_person_id,
			r.relation_type, r.confidence, r.note, r.created_by,
			r.created_at, r.updated_at, r.deleted_at, r.version
		FROM parent_child_relations r
		WHERE r.tree_id = $1
		  AND r.parent_person_id = ANY($2::uuid[])
		  AND r.child_person_id = ANY($2::uuid[])
		  AND r.deleted_at IS NULL
		ORDER BY r.created_at, r.id
	`, treeID, nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("load graph relations: %w", err)
	}
	defer rows.Close()
	relations := make([]domain.ParentChildRelation, 0)
	for rows.Next() {
		relation, err := scanRelation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan graph relation: %w", err)
		}
		relations = append(relations, relation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate graph relations: %w", err)
	}
	return relations, nil
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
		return fmt.Errorf("authorize editable relation tree: %w", err)
	}
	if !allowed {
		return domain.ErrRelationAccessDenied
	}
	return nil
}

func lockTreeGraph(ctx context.Context, tx pgx.Tx, treeID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended($1::text, 741926))
	`, treeID); err != nil {
		return fmt.Errorf("lock tree graph: %w", err)
	}
	return nil
}

func requireActivePersons(
	ctx context.Context,
	tx pgx.Tx,
	treeID uuid.UUID,
	parentPersonID uuid.UUID,
	childPersonID uuid.UUID,
) error {
	var count int
	err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM persons
		WHERE tree_id = $1
		  AND id = ANY($2::uuid[])
		  AND deleted_at IS NULL
	`, treeID, []uuid.UUID{parentPersonID, childPersonID}).Scan(&count)
	if err != nil {
		return fmt.Errorf("validate relation persons: %w", err)
	}
	if count != 2 {
		return domain.ErrRelationNotFound
	}
	return nil
}

func mapWriteError(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" &&
		postgresError.ConstraintName == relationUniqueConstraint {
		return domain.ErrDuplicateRelation
	}
	return fmt.Errorf("%s: %w", operation, err)
}

type scanner interface {
	Scan(...any) error
}

func scanRelation(row scanner) (domain.ParentChildRelation, error) {
	var relation domain.ParentChildRelation
	err := row.Scan(
		&relation.ID,
		&relation.TreeID,
		&relation.ParentPersonID,
		&relation.ChildPersonID,
		&relation.RelationType,
		&relation.Confidence,
		&relation.Note,
		&relation.CreatedBy,
		&relation.CreatedAt,
		&relation.UpdatedAt,
		&relation.DeletedAt,
		&relation.Version,
	)
	return relation, err
}
