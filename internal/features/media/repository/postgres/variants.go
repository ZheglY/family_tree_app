package postgres

import (
	"context"
	"fmt"

	"github.com/ZheglY/family_tree_app/internal/features/media/domain"
	"github.com/google/uuid"
)

func (r *Repository) ListVariantsAccessible(
	ctx context.Context,
	treeID uuid.UUID,
	actorUserID uuid.UUID,
	mediaIDs []uuid.UUID,
) (map[uuid.UUID][]domain.MediaVariant, error) {
	result := make(map[uuid.UUID][]domain.MediaVariant, len(mediaIDs))
	if len(mediaIDs) == 0 {
		return result, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT
			v.id, v.tree_id, v.media_id, v.kind, v.object_key,
			v.mime_type, v.size_bytes, v.checksum_sha256,
			v.width, v.height, v.created_at
		FROM media_variants v
		JOIN media_assets a
		  ON a.tree_id = v.tree_id
		 AND a.id = v.media_id
		 AND a.status = 'ready'
		 AND a.deleted_at IS NULL
		JOIN tree_members m
		  ON m.tree_id = v.tree_id
		 AND m.user_id = $2
		 AND m.status = 'active'
		JOIN family_trees t
		  ON t.id = v.tree_id
		 AND t.deleted_at IS NULL
		WHERE v.tree_id = $1
		  AND v.media_id = ANY($3::uuid[])
		ORDER BY v.media_id, v.kind
	`, treeID, actorUserID, mediaIDs)
	if err != nil {
		return nil, fmt.Errorf("list accessible media variants: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var variant domain.MediaVariant
		if err := rows.Scan(
			&variant.ID,
			&variant.TreeID,
			&variant.MediaID,
			&variant.Kind,
			&variant.ObjectKey,
			&variant.MIMEType,
			&variant.SizeBytes,
			&variant.ChecksumSHA256,
			&variant.Width,
			&variant.Height,
			&variant.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan accessible media variant: %w", err)
		}
		result[variant.MediaID] = append(result[variant.MediaID], variant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accessible media variants: %w", err)
	}
	return result, nil
}
