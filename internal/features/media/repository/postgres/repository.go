package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/ZheglY/family_tree_app/internal/core/jobs"
	jobpostgres "github.com/ZheglY/family_tree_app/internal/core/jobs/postgres"
	"github.com/ZheglY/family_tree_app/internal/features/media/domain"
	"github.com/ZheglY/family_tree_app/internal/features/media/mediajob"
	"github.com/ZheglY/family_tree_app/internal/features/media/service"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const personMediaPrimaryKey = "person_media_pkey"

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) CreateIntentEditable(
	ctx context.Context,
	actorUserID uuid.UUID,
	asset domain.MediaAsset,
) (domain.MediaAsset, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.MediaAsset{}, false, fmt.Errorf("begin media intent transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, asset.TreeID, actorUserID); err != nil {
		return domain.MediaAsset{}, false, err
	}
	result, err := tx.Exec(ctx, `
		INSERT INTO media_assets (
			id, tree_id, client_request_id, kind, status, object_key,
			original_filename, mime_type, size_bytes, checksum_sha256,
			etag, width, height, caption, description, uploaded_by,
			uploaded_at, created_at, updated_at, deleted_at, version
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10,
			'', NULL, NULL, '', '', $11,
			NULL, $12, $12, NULL, $13
		)
		ON CONFLICT (tree_id, client_request_id) DO NOTHING
	`,
		asset.ID,
		asset.TreeID,
		asset.ClientRequestID,
		asset.Kind,
		asset.Status,
		asset.ObjectKey,
		asset.OriginalFilename,
		asset.MIMEType,
		asset.SizeBytes,
		asset.ChecksumSHA256,
		actorUserID,
		asset.CreatedAt,
		asset.Version,
	)
	if err != nil {
		return domain.MediaAsset{}, false, fmt.Errorf("insert media upload intent: %w", err)
	}
	created := result.RowsAffected() == 1
	if !created {
		existing, err := scanAsset(tx.QueryRow(ctx, `
			SELECT
				id, tree_id, client_request_id, kind, status, object_key,
				original_filename, mime_type, size_bytes, checksum_sha256,
				etag, width, height, caption, description, uploaded_by,
				uploaded_at, created_at, updated_at, deleted_at,
				processing_error, processed_at, version
			FROM media_assets
			WHERE tree_id = $1 AND client_request_id = $2
		`, asset.TreeID, asset.ClientRequestID))
		if err != nil {
			return domain.MediaAsset{}, false, fmt.Errorf("load idempotent media intent: %w", err)
		}
		if existing.DeletedAt != nil || !sameUploadIntent(existing, asset) {
			return domain.MediaAsset{}, false, domain.ErrMediaRequestConflict
		}
		asset = existing
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MediaAsset{}, false, fmt.Errorf("commit media intent transaction: %w", err)
	}
	return asset, created, nil
}

func (r *Repository) GetAccessible(
	ctx context.Context,
	treeID uuid.UUID,
	mediaID uuid.UUID,
	actorUserID uuid.UUID,
) (domain.MediaAsset, error) {
	asset, err := scanAsset(r.pool.QueryRow(ctx, `
		SELECT
			a.id, a.tree_id, a.client_request_id, a.kind, a.status, a.object_key,
			a.original_filename, a.mime_type, a.size_bytes, a.checksum_sha256,
			a.etag, a.width, a.height, a.caption, a.description, a.uploaded_by,
			a.uploaded_at, a.created_at, a.updated_at, a.deleted_at,
			a.processing_error, a.processed_at, a.version
		FROM media_assets a
		JOIN tree_members m
		  ON m.tree_id = a.tree_id
		 AND m.user_id = $3
		 AND m.status = 'active'
		JOIN family_trees t
		  ON t.id = a.tree_id
		 AND t.deleted_at IS NULL
		WHERE a.tree_id = $1
		  AND a.id = $2
		  AND a.deleted_at IS NULL
	`, treeID, mediaID, actorUserID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MediaAsset{}, domain.ErrMediaNotFound
	}
	if err != nil {
		return domain.MediaAsset{}, fmt.Errorf("get accessible media: %w", err)
	}
	return asset, nil
}

func (r *Repository) ListAccessible(
	ctx context.Context,
	filter service.ListFilter,
) ([]domain.MediaAsset, error) {
	var beforeCreated any
	if !filter.BeforeCreated.IsZero() {
		beforeCreated = filter.BeforeCreated
	}
	rows, err := r.pool.Query(ctx, `
		SELECT
			a.id, a.tree_id, a.client_request_id, a.kind, a.status, a.object_key,
			a.original_filename, a.mime_type, a.size_bytes, a.checksum_sha256,
			a.etag, a.width, a.height, a.caption, a.description, a.uploaded_by,
			a.uploaded_at, a.created_at, a.updated_at, a.deleted_at,
			a.processing_error, a.processed_at, a.version
		FROM media_assets a
		JOIN tree_members m
		  ON m.tree_id = a.tree_id
		 AND m.user_id = $2
		 AND m.status = 'active'
		JOIN family_trees t
		  ON t.id = a.tree_id
		 AND t.deleted_at IS NULL
		WHERE a.tree_id = $1
		  AND a.deleted_at IS NULL
		  AND ($3 = '' OR a.kind = $3)
		  AND ($4 = '' OR a.status = $4)
		  AND (
			$5::timestamptz IS NULL OR
			(a.created_at, a.id) < ($5::timestamptz, $6::uuid)
		  )
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT $7
	`,
		filter.TreeID,
		filter.ActorUserID,
		filter.Kind,
		filter.Status,
		beforeCreated,
		filter.BeforeMediaID,
		filter.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list accessible media: %w", err)
	}
	defer rows.Close()
	assets := make([]domain.MediaAsset, 0)
	for rows.Next() {
		asset, err := scanAsset(rows)
		if err != nil {
			return nil, fmt.Errorf("scan listed media: %w", err)
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate listed media: %w", err)
	}
	return assets, nil
}

func (r *Repository) CompleteUploadEditable(
	ctx context.Context,
	actorUserID uuid.UUID,
	asset domain.MediaAsset,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin media completion transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, asset.TreeID, actorUserID); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `
		UPDATE media_assets
		SET status = $3,
			etag = $4,
			uploaded_at = $5,
			updated_at = $6,
			version = $7
		WHERE tree_id = $1
		  AND id = $2
		  AND status = 'pending'
		  AND deleted_at IS NULL
		  AND version = $7 - 1
	`,
		asset.TreeID,
		asset.ID,
		asset.Status,
		asset.ETag,
		asset.UploadedAt,
		asset.UpdatedAt,
		asset.Version,
	)
	if err != nil {
		return fmt.Errorf("complete media upload: %w", err)
	}
	if result.RowsAffected() != 1 {
		return domain.ErrMediaVersionConflict
	}
	payload, err := mediajob.Encode(mediajob.ProcessPayload{
		TreeID:  asset.TreeID,
		MediaID: asset.ID,
	})
	if err != nil {
		return err
	}
	if _, _, err := jobpostgres.EnqueueWith(ctx, tx, jobs.EnqueueRequest{
		ID:               uuid.New(),
		Kind:             mediajob.KindProcess,
		DeduplicationKey: asset.ID.String(),
		Payload:          payload,
		MaxAttempts:      5,
		AvailableAt:      asset.UpdatedAt,
		CreatedAt:        asset.UpdatedAt,
	}); err != nil {
		return fmt.Errorf("enqueue media processing: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit media completion transaction: %w", err)
	}
	return nil
}

func (r *Repository) UpdateEditable(
	ctx context.Context,
	actorUserID uuid.UUID,
	asset domain.MediaAsset,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin media update transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, asset.TreeID, actorUserID); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `
		UPDATE media_assets
		SET caption = $3,
			description = $4,
			updated_at = $5,
			version = $6
		WHERE tree_id = $1
		  AND id = $2
		  AND deleted_at IS NULL
		  AND version = $6 - 1
	`, asset.TreeID, asset.ID, asset.Caption, asset.Description, asset.UpdatedAt, asset.Version)
	if err != nil {
		return fmt.Errorf("update media metadata: %w", err)
	}
	if result.RowsAffected() != 1 {
		return domain.ErrMediaVersionConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit media update transaction: %w", err)
	}
	return nil
}

func (r *Repository) SoftDeleteEditable(
	ctx context.Context,
	mutation service.AuditMutation,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin media deletion transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, mutation.TreeID, mutation.ActorUserID); err != nil {
		return err
	}
	var newVersion int
	err = tx.QueryRow(ctx, `
		UPDATE media_assets
		SET status = 'deleted',
			deleted_at = $4,
			updated_at = $4,
			version = version + 1
		WHERE tree_id = $1
		  AND id = $2
		  AND version = $3
		  AND deleted_at IS NULL
		RETURNING version
	`,
		mutation.TreeID,
		mutation.MediaID,
		mutation.Version,
		mutation.OccurredAt,
	).Scan(&newVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrMediaVersionConflict
	}
	if err != nil {
		return fmt.Errorf("soft delete media: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE persons
		SET primary_media_id = NULL,
			updated_by = $2,
			updated_at = $4,
			version = version + 1
		WHERE tree_id = $1
		  AND primary_media_id = $3
	`, mutation.TreeID, mutation.ActorUserID, mutation.MediaID, mutation.OccurredAt); err != nil {
		return fmt.Errorf("clear deleted primary media: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (
			id, tree_id, actor_user_id, action, entity_type, entity_id,
			request_id, ip_address, changes, created_at
		) VALUES (
			$1, $2, $3, 'media_asset.deleted', 'media_asset', $4,
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
		mutation.MediaID,
		mutation.RequestID,
		mutation.IPAddress,
		mutation.Version,
		newVersion,
		mutation.OccurredAt,
	); err != nil {
		return fmt.Errorf("insert media deletion audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit media deletion transaction: %w", err)
	}
	return nil
}

func (r *Repository) AttachToPersonEditable(
	ctx context.Context,
	actorUserID uuid.UUID,
	attachment domain.PersonMedia,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin person media attachment transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, attachment.TreeID, actorUserID); err != nil {
		return err
	}
	var mediaStatus string
	err = tx.QueryRow(ctx, `
		SELECT status
		FROM media_assets
		WHERE tree_id = $1 AND id = $2 AND deleted_at IS NULL
		FOR SHARE
	`, attachment.TreeID, attachment.MediaID).Scan(&mediaStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrMediaNotFound
	}
	if err != nil {
		return fmt.Errorf("lock attachable media: %w", err)
	}
	if !domain.CanAttach(mediaStatus) {
		return domain.ErrMediaStateConflict
	}
	var personExists bool
	err = tx.QueryRow(ctx, `
		SELECT true
		FROM persons
		WHERE tree_id = $1 AND id = $2 AND deleted_at IS NULL
		FOR SHARE
	`, attachment.TreeID, attachment.PersonID).Scan(&personExists)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrMediaNotFound
	}
	if err != nil {
		return fmt.Errorf("validate media person: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO person_media (
			tree_id, person_id, media_id, role, sort_order, created_by, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`,
		attachment.TreeID,
		attachment.PersonID,
		attachment.MediaID,
		attachment.Role,
		attachment.SortOrder,
		attachment.CreatedBy,
		attachment.CreatedAt,
	)
	if err != nil {
		return mapAttachmentWriteError("attach media to person", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit person media attachment transaction: %w", err)
	}
	return nil
}

func (r *Repository) DetachFromPersonEditable(
	ctx context.Context,
	mutation service.AttachmentMutation,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin person media detachment transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, mutation.TreeID, mutation.ActorUserID); err != nil {
		return err
	}
	var personExists bool
	err = tx.QueryRow(ctx, `
		SELECT true
		FROM persons
		WHERE tree_id = $1 AND id = $2 AND deleted_at IS NULL
		FOR SHARE
	`, mutation.TreeID, mutation.PersonID).Scan(&personExists)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrMediaAttachmentNotFound
	}
	if err != nil {
		return fmt.Errorf("validate media person for detachment: %w", err)
	}
	result, err := tx.Exec(ctx, `
		DELETE FROM person_media
		WHERE tree_id = $1 AND person_id = $2 AND media_id = $3
	`, mutation.TreeID, mutation.PersonID, mutation.MediaID)
	if err != nil {
		return fmt.Errorf("detach media from person: %w", err)
	}
	if result.RowsAffected() != 1 {
		return domain.ErrMediaAttachmentNotFound
	}
	if _, err := tx.Exec(ctx, `
		UPDATE persons
		SET primary_media_id = NULL,
			updated_by = $4,
			updated_at = $5,
			version = version + 1
		WHERE tree_id = $1
		  AND id = $2
		  AND primary_media_id = $3
		  AND deleted_at IS NULL
	`,
		mutation.TreeID,
		mutation.PersonID,
		mutation.MediaID,
		mutation.ActorUserID,
		mutation.OccurredAt,
	); err != nil {
		return fmt.Errorf("clear detached primary media: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit person media detachment transaction: %w", err)
	}
	return nil
}

func (r *Repository) SetPrimaryPersonMediaEditable(
	ctx context.Context,
	mutation service.PrimaryMediaMutation,
) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin primary media transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireEditableTree(ctx, tx, mutation.TreeID, mutation.ActorUserID); err != nil {
		return 0, err
	}
	var attachmentExists bool
	err = tx.QueryRow(ctx, `
		SELECT true
		FROM person_media pm
		JOIN media_assets a
		  ON a.tree_id = pm.tree_id
		 AND a.id = pm.media_id
		 AND a.kind = 'photo'
		 AND a.status IN ('uploaded', 'processing', 'ready')
		 AND a.deleted_at IS NULL
		JOIN persons p
		  ON p.tree_id = pm.tree_id
		 AND p.id = pm.person_id
		 AND p.deleted_at IS NULL
		WHERE pm.tree_id = $1 AND pm.person_id = $2 AND pm.media_id = $3
		FOR SHARE OF pm, a, p
	`, mutation.TreeID, mutation.PersonID, mutation.MediaID).Scan(&attachmentExists)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrMediaAttachmentNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("validate primary person media: %w", err)
	}
	var newVersion int
	err = tx.QueryRow(ctx, `
		UPDATE persons
		SET primary_media_id = $3,
			updated_by = $4,
			updated_at = $6,
			version = version + 1
		WHERE tree_id = $1
		  AND id = $2
		  AND version = $5
		  AND deleted_at IS NULL
		RETURNING version
	`,
		mutation.TreeID,
		mutation.PersonID,
		mutation.MediaID,
		mutation.ActorUserID,
		mutation.PersonVersion,
		mutation.OccurredAt,
	).Scan(&newVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrPrimaryMediaConflict
	}
	if err != nil {
		return 0, fmt.Errorf("set primary person media: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit primary media transaction: %w", err)
	}
	return newVersion, nil
}

func sameUploadIntent(existing domain.MediaAsset, requested domain.MediaAsset) bool {
	return existing.ClientRequestID == requested.ClientRequestID &&
		existing.Kind == requested.Kind &&
		existing.OriginalFilename == requested.OriginalFilename &&
		existing.MIMEType == requested.MIMEType &&
		existing.SizeBytes == requested.SizeBytes &&
		existing.ChecksumSHA256 == requested.ChecksumSHA256
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
		return fmt.Errorf("authorize editable media tree: %w", err)
	}
	if !allowed {
		return domain.ErrMediaAccessDenied
	}
	return nil
}

func mapAttachmentWriteError(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		if postgresError.Code == "23505" && postgresError.ConstraintName == personMediaPrimaryKey {
			return domain.ErrDuplicateMediaAttachment
		}
		if postgresError.Code == "23503" {
			return domain.ErrMediaNotFound
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

type scanner interface {
	Scan(...any) error
}

func scanAsset(row scanner) (domain.MediaAsset, error) {
	var asset domain.MediaAsset
	err := row.Scan(
		&asset.ID,
		&asset.TreeID,
		&asset.ClientRequestID,
		&asset.Kind,
		&asset.Status,
		&asset.ObjectKey,
		&asset.OriginalFilename,
		&asset.MIMEType,
		&asset.SizeBytes,
		&asset.ChecksumSHA256,
		&asset.ETag,
		&asset.Width,
		&asset.Height,
		&asset.Caption,
		&asset.Description,
		&asset.UploadedBy,
		&asset.UploadedAt,
		&asset.CreatedAt,
		&asset.UpdatedAt,
		&asset.DeletedAt,
		&asset.ProcessingError,
		&asset.ProcessedAt,
		&asset.Version,
	)
	return asset, err
}
