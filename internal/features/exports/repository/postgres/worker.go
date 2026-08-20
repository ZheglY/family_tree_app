package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ZheglY/family_tree_app/internal/features/exports/domain"
	"github.com/ZheglY/family_tree_app/internal/features/exports/manifest"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (repository *Repository) AcquireForGeneration(
	ctx context.Context,
	treeID uuid.UUID,
	exportID uuid.UUID,
	now time.Time,
) (domain.Export, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return domain.Export{}, fmt.Errorf("begin export generation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	export, err := scanExport(tx.QueryRow(ctx, exportSelect+`
		WHERE tree_id = $1 AND id = $2
		FOR UPDATE
	`, treeID, exportID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Export{}, domain.ErrExportNotFound
	}
	if err != nil {
		return domain.Export{}, fmt.Errorf("lock export for generation: %w", err)
	}
	if export.Status == domain.StatusQueued {
		if _, err := tx.Exec(ctx, `
			UPDATE export_jobs
			SET status = 'running', progress = 10, error_code = '',
				started_at = COALESCE(started_at, $3), updated_at = $3
			WHERE tree_id = $1 AND id = $2
		`, treeID, exportID, now); err != nil {
			return domain.Export{}, fmt.Errorf("mark export running: %w", err)
		}
		export.Status = domain.StatusRunning
		export.Progress = 10
		export.UpdatedAt = now
		if export.StartedAt == nil {
			export.StartedAt = &now
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Export{}, fmt.Errorf("commit export generation start: %w", err)
	}
	return export, nil
}

func (repository *Repository) LoadManifest(
	ctx context.Context,
	export domain.Export,
) (manifest.Manifest, error) {
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("begin manifest snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result := manifest.Manifest{
		Schema: manifest.Schema{Name: domain.ManifestSchemaName, Version: export.SchemaVersion},
		Export: manifest.ExportMetadata{
			ID: export.ID, Format: export.Format, RequestedBy: export.RequestedBy, CreatedAt: export.CreatedAt,
		},
		Members:              make([]manifest.TreeMember, 0),
		Persons:              make([]manifest.Person, 0),
		PersonNames:          make([]manifest.PersonName, 0),
		ParentChildRelations: make([]manifest.ParentChildRelation, 0),
		Unions:               make([]manifest.FamilyUnion, 0),
		UnionMembers:         make([]manifest.UnionMember, 0),
		MediaAssets:          make([]manifest.MediaAsset, 0),
		MediaVariants:        make([]manifest.MediaVariant, 0),
		PersonMedia:          make([]manifest.PersonMediaAttachment, 0),
	}
	err = tx.QueryRow(ctx, `
		SELECT id, name, description, owner_user_id, root_person_id, cover_media_id,
			privacy, locale, timezone, created_at, updated_at, deleted_at, version
		FROM family_trees
		WHERE id = $1 AND deleted_at IS NULL
	`, export.TreeID).Scan(
		&result.Tree.ID, &result.Tree.Name, &result.Tree.Description, &result.Tree.OwnerUserID,
		&result.Tree.RootPersonID, &result.Tree.CoverMediaID, &result.Tree.Privacy,
		&result.Tree.Locale, &result.Tree.Timezone, &result.Tree.CreatedAt, &result.Tree.UpdatedAt,
		&result.Tree.DeletedAt, &result.Tree.Version,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return manifest.Manifest{}, domain.ErrExportTreeUnavailable
	}
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("load export tree snapshot: %w", err)
	}
	if err := loadMembers(ctx, tx, export.TreeID, &result); err != nil {
		return manifest.Manifest{}, err
	}
	if err := loadPersons(ctx, tx, export.TreeID, &result); err != nil {
		return manifest.Manifest{}, err
	}
	if err := loadRelations(ctx, tx, export.TreeID, &result); err != nil {
		return manifest.Manifest{}, err
	}
	if err := loadUnions(ctx, tx, export.TreeID, &result); err != nil {
		return manifest.Manifest{}, err
	}
	if err := loadMedia(ctx, tx, export.TreeID, &result); err != nil {
		return manifest.Manifest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return manifest.Manifest{}, fmt.Errorf("commit manifest snapshot: %w", err)
	}
	return result, nil
}

func loadMembers(ctx context.Context, tx pgx.Tx, treeID uuid.UUID, result *manifest.Manifest) error {
	rows, err := tx.Query(ctx, `
		SELECT user_id, role, status, invited_by, created_at, accepted_at
		FROM tree_members WHERE tree_id = $1 ORDER BY created_at, user_id
	`, treeID)
	if err != nil {
		return fmt.Errorf("load export members: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item manifest.TreeMember
		if err := rows.Scan(&item.UserID, &item.Role, &item.Status, &item.InvitedBy, &item.CreatedAt, &item.AcceptedAt); err != nil {
			return fmt.Errorf("scan export member: %w", err)
		}
		result.Members = append(result.Members, item)
	}
	return rows.Err()
}

func loadPersons(ctx context.Context, tx pgx.Tx, treeID uuid.UUID, result *manifest.Manifest) error {
	rows, err := tx.Query(ctx, `
		SELECT id, sex, life_status, biography, notes, primary_media_id, privacy_level,
			created_by, updated_by, created_at, updated_at, deleted_at, version
		FROM persons WHERE tree_id = $1 ORDER BY created_at, id
	`, treeID)
	if err != nil {
		return fmt.Errorf("load export persons: %w", err)
	}
	for rows.Next() {
		var item manifest.Person
		if err := rows.Scan(&item.ID, &item.Sex, &item.LifeStatus, &item.Biography, &item.Notes,
			&item.PrimaryMediaID, &item.PrivacyLevel, &item.CreatedBy, &item.UpdatedBy,
			&item.CreatedAt, &item.UpdatedAt, &item.DeletedAt, &item.Version); err != nil {
			rows.Close()
			return fmt.Errorf("scan export person: %w", err)
		}
		result.Persons = append(result.Persons, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate export persons: %w", err)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `
		SELECT id, person_id, type, given_name, patronymic, family_name, prefix, suffix,
			full_text, is_preferred, language_code, created_at, updated_at
		FROM person_names WHERE tree_id = $1 ORDER BY created_at, id
	`, treeID)
	if err != nil {
		return fmt.Errorf("load export person names: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item manifest.PersonName
		if err := rows.Scan(&item.ID, &item.PersonID, &item.Type, &item.GivenName, &item.Patronymic,
			&item.FamilyName, &item.Prefix, &item.Suffix, &item.FullText, &item.IsPreferred,
			&item.LanguageCode, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return fmt.Errorf("scan export person name: %w", err)
		}
		result.PersonNames = append(result.PersonNames, item)
	}
	return rows.Err()
}

func loadRelations(ctx context.Context, tx pgx.Tx, treeID uuid.UUID, result *manifest.Manifest) error {
	rows, err := tx.Query(ctx, `
		SELECT id, parent_person_id, child_person_id, relation_type, confidence, note,
			created_by, created_at, updated_at, deleted_at, version
		FROM parent_child_relations WHERE tree_id = $1 ORDER BY created_at, id
	`, treeID)
	if err != nil {
		return fmt.Errorf("load export relations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item manifest.ParentChildRelation
		if err := rows.Scan(&item.ID, &item.ParentPersonID, &item.ChildPersonID, &item.RelationType,
			&item.Confidence, &item.Note, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
			&item.DeletedAt, &item.Version); err != nil {
			return fmt.Errorf("scan export relation: %w", err)
		}
		result.ParentChildRelations = append(result.ParentChildRelations, item)
	}
	return rows.Err()
}

func loadUnions(ctx context.Context, tx pgx.Tx, treeID uuid.UUID, result *manifest.Manifest) error {
	rows, err := tx.Query(ctx, `
		SELECT id, type, end_reason, note, created_by, updated_by, created_at, updated_at, deleted_at, version
		FROM family_unions WHERE tree_id = $1 ORDER BY created_at, id
	`, treeID)
	if err != nil {
		return fmt.Errorf("load export unions: %w", err)
	}
	for rows.Next() {
		var item manifest.FamilyUnion
		if err := rows.Scan(&item.ID, &item.Type, &item.EndReason, &item.Note, &item.CreatedBy,
			&item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt, &item.DeletedAt, &item.Version); err != nil {
			rows.Close()
			return fmt.Errorf("scan export union: %w", err)
		}
		result.Unions = append(result.Unions, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate export unions: %w", err)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `
		SELECT union_id, person_id, role, created_at
		FROM union_members WHERE tree_id = $1 ORDER BY created_at, union_id, person_id
	`, treeID)
	if err != nil {
		return fmt.Errorf("load export union members: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item manifest.UnionMember
		if err := rows.Scan(&item.UnionID, &item.PersonID, &item.Role, &item.CreatedAt); err != nil {
			return fmt.Errorf("scan export union member: %w", err)
		}
		result.UnionMembers = append(result.UnionMembers, item)
	}
	return rows.Err()
}

func loadMedia(ctx context.Context, tx pgx.Tx, treeID uuid.UUID, result *manifest.Manifest) error {
	rows, err := tx.Query(ctx, `
		SELECT id, kind, status, original_filename, mime_type, size_bytes, checksum_sha256,
			width, height, caption, description, uploaded_by, uploaded_at, created_at,
			updated_at, deleted_at, processed_at, version
		FROM media_assets WHERE tree_id = $1 ORDER BY created_at, id
	`, treeID)
	if err != nil {
		return fmt.Errorf("load export media: %w", err)
	}
	for rows.Next() {
		var item manifest.MediaAsset
		if err := rows.Scan(&item.ID, &item.Kind, &item.Status, &item.OriginalFilename, &item.MIMEType,
			&item.SizeBytes, &item.ChecksumSHA256, &item.Width, &item.Height, &item.Caption,
			&item.Description, &item.UploadedBy, &item.UploadedAt, &item.CreatedAt, &item.UpdatedAt,
			&item.DeletedAt, &item.ProcessedAt, &item.Version); err != nil {
			rows.Close()
			return fmt.Errorf("scan export media: %w", err)
		}
		result.MediaAssets = append(result.MediaAssets, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate export media: %w", err)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `
		SELECT id, media_id, kind, mime_type, size_bytes, checksum_sha256, width, height, created_at
		FROM media_variants WHERE tree_id = $1 ORDER BY created_at, id
	`, treeID)
	if err != nil {
		return fmt.Errorf("load export media variants: %w", err)
	}
	for rows.Next() {
		var item manifest.MediaVariant
		if err := rows.Scan(&item.ID, &item.MediaID, &item.Kind, &item.MIMEType, &item.SizeBytes,
			&item.ChecksumSHA256, &item.Width, &item.Height, &item.CreatedAt); err != nil {
			rows.Close()
			return fmt.Errorf("scan export media variant: %w", err)
		}
		result.MediaVariants = append(result.MediaVariants, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate export media variants: %w", err)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `
		SELECT person_id, media_id, role, sort_order, created_by, created_at
		FROM person_media WHERE tree_id = $1 ORDER BY created_at, person_id, media_id
	`, treeID)
	if err != nil {
		return fmt.Errorf("load export person media: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item manifest.PersonMediaAttachment
		if err := rows.Scan(&item.PersonID, &item.MediaID, &item.Role, &item.SortOrder, &item.CreatedBy, &item.CreatedAt); err != nil {
			return fmt.Errorf("scan export person media: %w", err)
		}
		result.PersonMedia = append(result.PersonMedia, item)
	}
	return rows.Err()
}

func (repository *Repository) MarkCompleted(
	ctx context.Context,
	export domain.Export,
	objectKey string,
	mimeType string,
	sizeBytes int64,
	checksum string,
	expiresAt time.Time,
	now time.Time,
) error {
	result, err := repository.pool.Exec(ctx, `
		UPDATE export_jobs
		SET status = 'completed', progress = 100,
			result_object_key = $3, result_mime_type = $4, result_size_bytes = $5,
			result_checksum_sha256 = $6, error_code = '', updated_at = $8,
			finished_at = $8, expires_at = $7
		WHERE tree_id = $1 AND id = $2 AND status = 'running'
	`, export.TreeID, export.ID, objectKey, mimeType, sizeBytes, checksum, expiresAt, now)
	if err != nil {
		return fmt.Errorf("mark export completed: %w", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	var status string
	var storedObjectKey string
	var storedChecksum string
	if err := repository.pool.QueryRow(ctx, `
		SELECT status, result_object_key, result_checksum_sha256
		FROM export_jobs WHERE tree_id = $1 AND id = $2
	`, export.TreeID, export.ID).Scan(
		&status, &storedObjectKey, &storedChecksum,
	); errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrExportNotFound
	} else if err != nil {
		return fmt.Errorf("load export completion state: %w", err)
	}
	if status == domain.StatusCompleted && storedObjectKey == objectKey && storedChecksum == checksum {
		return nil
	}
	return domain.ErrExportStateConflict
}

func (repository *Repository) MarkFailed(
	ctx context.Context,
	export domain.Export,
	errorCode string,
	now time.Time,
) error {
	result, err := repository.pool.Exec(ctx, `
		UPDATE export_jobs
		SET status = 'failed', error_code = $3, updated_at = $4, finished_at = $4
		WHERE tree_id = $1 AND id = $2 AND status IN ('queued', 'running')
	`, export.TreeID, export.ID, errorCode, now)
	if err != nil {
		return fmt.Errorf("mark export failed: %w", err)
	}
	if result.RowsAffected() == 0 {
		var status string
		if err := repository.pool.QueryRow(ctx, `SELECT status FROM export_jobs WHERE tree_id = $1 AND id = $2`,
			export.TreeID, export.ID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrExportNotFound
		} else if err != nil {
			return fmt.Errorf("load failed export state: %w", err)
		}
		if status != domain.StatusFailed && status != domain.StatusExpired {
			return domain.ErrExportStateConflict
		}
	}
	return nil
}

func (repository *Repository) ReserveCleanupCandidates(
	ctx context.Context,
	expiresBefore time.Time,
	limit int,
	now time.Time,
) ([]domain.CleanupCandidate, error) {
	rows, err := repository.pool.Query(ctx, `
		WITH candidates AS (
			SELECT id
			FROM export_jobs
			WHERE (status = 'completed' AND expires_at <= $1)
			   OR (status = 'expired' AND result_object_key <> '')
			ORDER BY expires_at NULLS FIRST, id
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE export_jobs AS export
		SET status = 'expired', updated_at = $3,
			finished_at = COALESCE(export.finished_at, $3),
			expires_at = COALESCE(export.expires_at, $3)
		FROM candidates
		WHERE export.id = candidates.id
		RETURNING export.tree_id, export.id, export.result_object_key
	`, expiresBefore, limit, now)
	if err != nil {
		return nil, fmt.Errorf("reserve expired exports: %w", err)
	}
	defer rows.Close()
	items := make([]domain.CleanupCandidate, 0)
	for rows.Next() {
		var item domain.CleanupCandidate
		if err := rows.Scan(&item.TreeID, &item.ExportID, &item.ResultObjectKey); err != nil {
			return nil, fmt.Errorf("scan expired export: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired exports: %w", err)
	}
	return items, nil
}

func (repository *Repository) GetDeletionCandidate(
	ctx context.Context,
	treeID uuid.UUID,
	exportID uuid.UUID,
) (domain.CleanupCandidate, error) {
	var item domain.CleanupCandidate
	err := repository.pool.QueryRow(ctx, `
		SELECT tree_id, id, result_object_key
		FROM export_jobs WHERE tree_id = $1 AND id = $2 AND status = 'expired'
	`, treeID, exportID).Scan(&item.TreeID, &item.ExportID, &item.ResultObjectKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CleanupCandidate{}, domain.ErrExportNotFound
	}
	if err != nil {
		return domain.CleanupCandidate{}, fmt.Errorf("load export deletion candidate: %w", err)
	}
	return item, nil
}

func (repository *Repository) ClearExpiredResult(
	ctx context.Context,
	candidate domain.CleanupCandidate,
	now time.Time,
) error {
	_, err := repository.pool.Exec(ctx, `
		UPDATE export_jobs
		SET result_object_key = '', result_mime_type = '', result_size_bytes = 0,
			result_checksum_sha256 = '', updated_at = $3
		WHERE tree_id = $1 AND id = $2 AND status = 'expired'
	`, candidate.TreeID, candidate.ExportID, now)
	if err != nil {
		return fmt.Errorf("clear expired export result: %w", err)
	}
	return nil
}
