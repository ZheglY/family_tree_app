package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ZheglY/family_tree_app/internal/features/media/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) AcquireForProcessing(
	ctx context.Context,
	treeID uuid.UUID,
	mediaID uuid.UUID,
	now time.Time,
) (domain.MediaAsset, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.MediaAsset{}, fmt.Errorf("begin media processing transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	asset, err := scanAsset(tx.QueryRow(ctx, workerAssetSelect+`
		WHERE tree_id = $1 AND id = $2
		FOR UPDATE
	`, treeID, mediaID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MediaAsset{}, domain.ErrMediaNotFound
	}
	if err != nil {
		return domain.MediaAsset{}, fmt.Errorf("lock media for processing: %w", err)
	}
	if asset.DeletedAt != nil || asset.Status == domain.StatusDeleted ||
		asset.Status == domain.StatusReady || asset.Status == domain.StatusRejected ||
		asset.Status == domain.StatusProcessing {
		if err := tx.Commit(ctx); err != nil {
			return domain.MediaAsset{}, fmt.Errorf("commit media processing read: %w", err)
		}
		return asset, nil
	}
	if asset.Status != domain.StatusUploaded {
		return domain.MediaAsset{}, domain.ErrMediaStateConflict
	}
	asset.Status = domain.StatusProcessing
	asset.ProcessingError = ""
	asset.UpdatedAt = now
	asset.Version++
	if _, err := tx.Exec(ctx, `
		UPDATE media_assets
		SET status = 'processing',
			processing_error = '',
			updated_at = $3,
			version = version + 1
		WHERE tree_id = $1 AND id = $2 AND status = 'uploaded' AND deleted_at IS NULL
	`, treeID, mediaID, now); err != nil {
		return domain.MediaAsset{}, fmt.Errorf("mark media processing: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.MediaAsset{}, fmt.Errorf("commit media processing start: %w", err)
	}
	return asset, nil
}

func (r *Repository) MarkProcessingReady(
	ctx context.Context,
	asset domain.MediaAsset,
	variants []domain.MediaVariant,
	width *int,
	height *int,
	now time.Time,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin ready media transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	err = tx.QueryRow(ctx, `
		SELECT status
		FROM media_assets
		WHERE tree_id = $1 AND id = $2
		FOR UPDATE
	`, asset.TreeID, asset.ID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrMediaNotFound
	}
	if err != nil {
		return fmt.Errorf("lock processed media: %w", err)
	}
	if status == domain.StatusReady {
		return tx.Commit(ctx)
	}
	if status != domain.StatusProcessing {
		return domain.ErrMediaStateConflict
	}
	for _, variant := range variants {
		if _, err := tx.Exec(ctx, `
			INSERT INTO media_variants (
				id, tree_id, media_id, kind, object_key, mime_type,
				size_bytes, checksum_sha256, width, height, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (media_id, kind) DO UPDATE
			SET object_key = EXCLUDED.object_key,
				mime_type = EXCLUDED.mime_type,
				size_bytes = EXCLUDED.size_bytes,
				checksum_sha256 = EXCLUDED.checksum_sha256,
				width = EXCLUDED.width,
				height = EXCLUDED.height,
				created_at = EXCLUDED.created_at
		`,
			variant.ID,
			variant.TreeID,
			variant.MediaID,
			variant.Kind,
			variant.ObjectKey,
			variant.MIMEType,
			variant.SizeBytes,
			variant.ChecksumSHA256,
			variant.Width,
			variant.Height,
			variant.CreatedAt,
		); err != nil {
			return fmt.Errorf("upsert media variant: %w", err)
		}
	}
	result, err := tx.Exec(ctx, `
		UPDATE media_assets
		SET status = 'ready',
			width = $3,
			height = $4,
			processing_error = '',
			processed_at = $5,
			updated_at = $5,
			version = version + 1
		WHERE tree_id = $1 AND id = $2 AND status = 'processing' AND deleted_at IS NULL
	`, asset.TreeID, asset.ID, width, height, now)
	if err != nil {
		return fmt.Errorf("mark media ready: %w", err)
	}
	if result.RowsAffected() != 1 {
		return domain.ErrMediaStateConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit ready media: %w", err)
	}
	return nil
}

func (r *Repository) RejectProcessing(
	ctx context.Context,
	asset domain.MediaAsset,
	reason string,
	now time.Time,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reject media transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if len(reason) > 1000 {
		reason = reason[:1000]
	}
	result, err := tx.Exec(ctx, `
		UPDATE media_assets
		SET status = 'rejected',
			processing_error = $3,
			processed_at = $4,
			updated_at = $4,
			version = version + 1
		WHERE tree_id = $1 AND id = $2 AND status = 'processing' AND deleted_at IS NULL
	`, asset.TreeID, asset.ID, reason, now)
	if err != nil {
		return fmt.Errorf("mark media rejected: %w", err)
	}
	if result.RowsAffected() == 0 {
		var status string
		err := tx.QueryRow(ctx, `
			SELECT status FROM media_assets WHERE tree_id = $1 AND id = $2
		`, asset.TreeID, asset.ID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrMediaNotFound
		}
		if err != nil {
			return fmt.Errorf("load rejected media state: %w", err)
		}
		if status == domain.StatusRejected || status == domain.StatusDeleted {
			return tx.Commit(ctx)
		}
		return domain.ErrMediaStateConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE persons
		SET primary_media_id = NULL,
			updated_at = $3,
			version = version + 1
		WHERE tree_id = $1 AND primary_media_id = $2
	`, asset.TreeID, asset.ID, now); err != nil {
		return fmt.Errorf("clear rejected primary media: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM person_media WHERE tree_id = $1 AND media_id = $2
	`, asset.TreeID, asset.ID); err != nil {
		return fmt.Errorf("detach rejected media: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit rejected media: %w", err)
	}
	return nil
}

func (r *Repository) ListCleanupCandidates(
	ctx context.Context,
	pendingBefore time.Time,
	deletedBefore time.Time,
	limit int,
) ([]domain.CleanupCandidate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, status, object_key
		FROM media_assets
		WHERE (status = 'pending' AND created_at <= $1)
		   OR (status = 'deleted' AND deleted_at <= $2)
		   OR (status = 'rejected' AND processed_at <= $2)
		ORDER BY updated_at, id
		LIMIT $3
	`, pendingBefore, deletedBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("list stale media: %w", err)
	}
	defer rows.Close()
	candidates := make([]domain.CleanupCandidate, 0)
	for rows.Next() {
		var candidate domain.CleanupCandidate
		if err := rows.Scan(&candidate.MediaID, &candidate.Status, &candidate.ObjectKey); err != nil {
			return nil, fmt.Errorf("scan stale media: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stale media: %w", err)
	}
	for index := range candidates {
		variantRows, err := r.pool.Query(ctx, `
			SELECT object_key FROM media_variants WHERE media_id = $1 ORDER BY kind
		`, candidates[index].MediaID)
		if err != nil {
			return nil, fmt.Errorf("list stale media variants: %w", err)
		}
		for variantRows.Next() {
			var objectKey string
			if err := variantRows.Scan(&objectKey); err != nil {
				variantRows.Close()
				return nil, fmt.Errorf("scan stale media variant: %w", err)
			}
			candidates[index].VariantKeys = append(candidates[index].VariantKeys, objectKey)
		}
		if err := variantRows.Err(); err != nil {
			variantRows.Close()
			return nil, fmt.Errorf("iterate stale media variants: %w", err)
		}
		variantRows.Close()
	}
	return candidates, nil
}

func (r *Repository) DeleteCleanupCandidate(
	ctx context.Context,
	candidate domain.CleanupCandidate,
	pendingBefore time.Time,
	deletedBefore time.Time,
) error {
	if candidate.Status == domain.StatusPending {
		return nil
	}
	result, err := r.pool.Exec(ctx, `
		DELETE FROM media_assets
		WHERE id = $1
		  AND (
			(status = 'pending' AND created_at <= $2) OR
			(status = 'deleted' AND deleted_at <= $3) OR
			(status = 'rejected' AND processed_at <= $3)
		  )
	`, candidate.MediaID, pendingBefore, deletedBefore)
	if err != nil {
		return fmt.Errorf("delete stale media metadata: %w", err)
	}
	if result.RowsAffected() > 1 {
		return fmt.Errorf("delete stale media metadata: unexpected row count")
	}
	return nil
}

func (r *Repository) ReserveCleanupCandidate(
	ctx context.Context,
	candidate domain.CleanupCandidate,
	pendingBefore time.Time,
	now time.Time,
) (bool, error) {
	if candidate.Status == domain.StatusDeleted || candidate.Status == domain.StatusRejected {
		return true, nil
	}
	if candidate.Status != domain.StatusPending {
		return false, nil
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE media_assets
		SET status = 'deleted',
			processing_error = 'upload expired before completion',
			deleted_at = $3,
			updated_at = $3,
			version = version + 1
		WHERE id = $1 AND status = 'pending' AND created_at <= $2 AND deleted_at IS NULL
	`, candidate.MediaID, pendingBefore, now)
	if err != nil {
		return false, fmt.Errorf("reserve stale media cleanup: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

const workerAssetSelect = `
	SELECT
		id, tree_id, client_request_id, kind, status, object_key,
		original_filename, mime_type, size_bytes, checksum_sha256,
		etag, width, height, caption, description, uploaded_by,
		uploaded_at, created_at, updated_at, deleted_at,
		processing_error, processed_at, version
	FROM media_assets
`
