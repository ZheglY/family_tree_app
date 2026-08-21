package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/jobs"
	jobpostgres "github.com/ZheglY/family_tree_app/internal/core/jobs/postgres"
	"github.com/ZheglY/family_tree_app/internal/features/exports/domain"
	"github.com/ZheglY/family_tree_app/internal/features/exports/exportjob"
	"github.com/ZheglY/family_tree_app/internal/features/exports/service"
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

func (repository *Repository) CreateAccessible(
	ctx context.Context,
	export domain.Export,
	audit service.AuditContext,
) (domain.Export, bool, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return domain.Export{}, false, fmt.Errorf("begin export creation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireExportCreator(ctx, tx, export.TreeID, export.RequestedBy); err != nil {
		return domain.Export{}, false, err
	}
	result, err := tx.Exec(ctx, `
		INSERT INTO export_jobs (
			id, tree_id, client_request_id, requested_by, format,
			schema_version, parameters, status, progress,
			result_object_key, result_mime_type, result_size_bytes,
			result_checksum_sha256, error_code,
			created_at, updated_at, started_at, finished_at, expires_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, 'queued', 0,
			'', '', 0,
			'', '',
			$8, $8, NULL, NULL, NULL
		)
		ON CONFLICT (tree_id, requested_by, client_request_id) DO NOTHING
	`,
		export.ID,
		export.TreeID,
		export.ClientRequestID,
		export.RequestedBy,
		export.Format,
		export.SchemaVersion,
		export.Parameters,
		export.CreatedAt,
	)
	if err != nil {
		return domain.Export{}, false, fmt.Errorf("insert export job: %w", err)
	}
	created := result.RowsAffected() == 1
	if !created {
		existing, err := scanExport(tx.QueryRow(ctx, exportSelect+`
			WHERE tree_id = $1 AND requested_by = $2 AND client_request_id = $3
		`, export.TreeID, export.RequestedBy, export.ClientRequestID))
		if err != nil {
			return domain.Export{}, false, fmt.Errorf("load idempotent export: %w", err)
		}
		if existing.Format != export.Format || existing.SchemaVersion != export.SchemaVersion ||
			!sameJSON(existing.Parameters, export.Parameters) {
			return domain.Export{}, false, domain.ErrExportRequestConflict
		}
		export = existing
	} else {
		payload, err := exportjob.Encode(exportjob.GeneratePayload{
			TreeID:   export.TreeID,
			ExportID: export.ID,
		})
		if err != nil {
			return domain.Export{}, false, err
		}
		if _, _, err := jobpostgres.EnqueueWith(ctx, tx, jobs.EnqueueRequest{
			ID:               uuid.New(),
			Kind:             exportjob.KindGenerate,
			DeduplicationKey: export.ID.String(),
			Payload:          payload,
			MaxAttempts:      5,
			AvailableAt:      export.CreatedAt,
			CreatedAt:        export.CreatedAt,
		}); err != nil {
			return domain.Export{}, false, fmt.Errorf("enqueue export generation: %w", err)
		}
		if err := insertAudit(ctx, tx, auditRecord{
			ID: audit.AuditID, TreeID: export.TreeID, ActorUserID: export.RequestedBy,
			Action: "export.created", ExportID: export.ID,
			RequestID: audit.RequestID, IPAddress: audit.IPAddress,
			Changes:   map[string]any{"format": export.Format, "schema_version": export.SchemaVersion},
			CreatedAt: export.CreatedAt,
		}); err != nil {
			return domain.Export{}, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Export{}, false, fmt.Errorf("commit export creation transaction: %w", err)
	}
	return export, created, nil
}

func (repository *Repository) GetAccessible(
	ctx context.Context,
	treeID uuid.UUID,
	exportID uuid.UUID,
	actorUserID uuid.UUID,
) (domain.Export, error) {
	export, err := scanExport(repository.pool.QueryRow(ctx, exportSelect+`
		JOIN tree_members member
		  ON member.tree_id = export_jobs.tree_id
		 AND member.user_id = $3
		 AND member.status = 'active'
		JOIN family_trees tree
		  ON tree.id = export_jobs.tree_id
		 AND tree.deleted_at IS NULL
		WHERE export_jobs.tree_id = $1
		  AND export_jobs.id = $2
		  AND (export_jobs.requested_by = $3 OR member.role = 'owner')
	`, treeID, exportID, actorUserID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Export{}, domain.ErrExportNotFound
	}
	if err != nil {
		return domain.Export{}, fmt.Errorf("get accessible export: %w", err)
	}
	return export, nil
}

func (repository *Repository) ListAccessible(
	ctx context.Context,
	filter service.ListFilter,
) ([]domain.Export, error) {
	var beforeCreated any
	if !filter.BeforeCreated.IsZero() {
		beforeCreated = filter.BeforeCreated
	}
	rows, err := repository.pool.Query(ctx, exportSelect+`
		JOIN tree_members member
		  ON member.tree_id = export_jobs.tree_id
		 AND member.user_id = $2
		 AND member.status = 'active'
		JOIN family_trees tree
		  ON tree.id = export_jobs.tree_id
		 AND tree.deleted_at IS NULL
		WHERE export_jobs.tree_id = $1
		  AND (export_jobs.requested_by = $2 OR member.role = 'owner')
		  AND (
			$3::timestamptz IS NULL OR
			(export_jobs.created_at, export_jobs.id) < ($3::timestamptz, $4::uuid)
		  )
		ORDER BY export_jobs.created_at DESC, export_jobs.id DESC
		LIMIT $5
	`,
		filter.TreeID,
		filter.ActorUserID,
		beforeCreated,
		filter.BeforeExportID,
		filter.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list accessible exports: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Export, 0)
	for rows.Next() {
		export, err := scanExport(rows)
		if err != nil {
			return nil, fmt.Errorf("scan listed export: %w", err)
		}
		items = append(items, export)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate listed exports: %w", err)
	}
	return items, nil
}

func (repository *Repository) ExpireAccessible(
	ctx context.Context,
	mutation service.MutationContext,
) error {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin export deletion transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var requestedBy uuid.UUID
	var role string
	var status string
	err = tx.QueryRow(ctx, `
		SELECT export_jobs.requested_by, member.role, export_jobs.status
		FROM export_jobs
		JOIN tree_members member
		  ON member.tree_id = export_jobs.tree_id
		 AND member.user_id = $3
		 AND member.status = 'active'
		JOIN family_trees tree
		  ON tree.id = export_jobs.tree_id
		 AND tree.deleted_at IS NULL
		WHERE export_jobs.tree_id = $1 AND export_jobs.id = $2
		FOR UPDATE OF export_jobs
	`, mutation.TreeID, mutation.ExportID, mutation.ActorUserID).Scan(&requestedBy, &role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrExportNotFound
	}
	if err != nil {
		return fmt.Errorf("lock accessible export for deletion: %w", err)
	}
	if requestedBy != mutation.ActorUserID && role != "owner" {
		return domain.ErrExportNotFound
	}
	transitioned := status != domain.StatusExpired
	if transitioned {
		if _, err := tx.Exec(ctx, `
			UPDATE export_jobs
			SET status = 'expired',
				updated_at = $3,
				finished_at = COALESCE(finished_at, $3),
				expires_at = COALESCE(expires_at, $3)
			WHERE tree_id = $1 AND id = $2
		`, mutation.TreeID, mutation.ExportID, mutation.OccurredAt); err != nil {
			return fmt.Errorf("expire export: %w", err)
		}
	}
	payload, err := exportjob.Encode(exportjob.DeletePayload{
		TreeID:   mutation.TreeID,
		ExportID: mutation.ExportID,
	})
	if err != nil {
		return err
	}
	if _, _, err := jobpostgres.EnqueueWith(ctx, tx, jobs.EnqueueRequest{
		ID:               uuid.New(),
		Kind:             exportjob.KindDelete,
		DeduplicationKey: mutation.ExportID.String(),
		Payload:          payload,
		MaxAttempts:      5,
		AvailableAt:      mutation.OccurredAt,
		CreatedAt:        mutation.OccurredAt,
	}); err != nil {
		return fmt.Errorf("enqueue export deletion: %w", err)
	}
	if transitioned {
		if err := insertAudit(ctx, tx, auditRecord{
			ID: mutation.AuditID, TreeID: mutation.TreeID, ActorUserID: mutation.ActorUserID,
			Action: "export.deleted", ExportID: mutation.ExportID,
			RequestID: mutation.RequestID, IPAddress: mutation.IPAddress,
			Changes: map[string]any{"previous_status": status}, CreatedAt: mutation.OccurredAt,
		}); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit export deletion transaction: %w", err)
	}
	return nil
}

func (repository *Repository) RecordDownload(
	ctx context.Context,
	audit service.DownloadAudit,
) error {
	result, err := repository.pool.Exec(ctx, `
		INSERT INTO audit_log (
			id, tree_id, actor_user_id, action, entity_type, entity_id,
			request_id, ip_address, changes, created_at
		)
		SELECT
			$1, export_jobs.tree_id, $2, 'export.downloaded', 'export_job', export_jobs.id,
			$5, NULLIF($6, '')::inet, '{}'::jsonb, $7
		FROM export_jobs
		JOIN tree_members member
		  ON member.tree_id = export_jobs.tree_id
		 AND member.user_id = $2
		 AND member.status = 'active'
		JOIN family_trees tree
		  ON tree.id = export_jobs.tree_id
		 AND tree.deleted_at IS NULL
		WHERE export_jobs.tree_id = $3
		  AND export_jobs.id = $4
		  AND (export_jobs.requested_by = $2 OR member.role = 'owner')
		  AND export_jobs.status = 'completed'
		  AND export_jobs.expires_at > $7
	`,
		audit.AuditID,
		audit.ActorUserID,
		audit.TreeID,
		audit.ExportID,
		audit.RequestID,
		audit.IPAddress,
		audit.OccurredAt,
	)
	if err != nil {
		return fmt.Errorf("record export download audit: %w", err)
	}
	if result.RowsAffected() != 1 {
		return domain.ErrExportResultUnavailable
	}
	return nil
}

func requireExportCreator(
	ctx context.Context,
	tx pgx.Tx,
	treeID uuid.UUID,
	actorUserID uuid.UUID,
) error {
	var allowed bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM tree_members member
			JOIN family_trees tree ON tree.id = member.tree_id
			WHERE member.tree_id = $1
			  AND member.user_id = $2
			  AND member.status = 'active'
			  AND member.role IN ('owner', 'editor')
			  AND tree.deleted_at IS NULL
		)
	`, treeID, actorUserID).Scan(&allowed); err != nil {
		return fmt.Errorf("authorize export creation: %w", err)
	}
	if !allowed {
		return domain.ErrExportAccessDenied
	}
	return nil
}

type auditRecord struct {
	ID          uuid.UUID
	TreeID      uuid.UUID
	ActorUserID uuid.UUID
	Action      string
	ExportID    uuid.UUID
	RequestID   string
	IPAddress   string
	Changes     map[string]any
	CreatedAt   time.Time
}

func insertAudit(ctx context.Context, tx pgx.Tx, record auditRecord) error {
	changes, err := json.Marshal(record.Changes)
	if err != nil {
		return fmt.Errorf("encode export audit changes: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (
			id, tree_id, actor_user_id, action, entity_type, entity_id,
			request_id, ip_address, changes, created_at
		) VALUES ($1, $2, $3, $4, 'export_job', $5, $6, NULLIF($7, '')::inet, $8, $9)
	`, record.ID, record.TreeID, record.ActorUserID, record.Action, record.ExportID,
		record.RequestID, record.IPAddress, changes, record.CreatedAt); err != nil {
		return fmt.Errorf("insert export audit: %w", err)
	}
	return nil
}

const exportSelect = `
	SELECT
		export_jobs.id, export_jobs.tree_id, export_jobs.client_request_id,
		export_jobs.requested_by, export_jobs.format,
		export_jobs.schema_version, export_jobs.parameters, export_jobs.status, export_jobs.progress,
		export_jobs.result_object_key, export_jobs.result_mime_type, export_jobs.result_size_bytes,
		export_jobs.result_checksum_sha256, export_jobs.error_code,
		export_jobs.created_at, export_jobs.updated_at, export_jobs.started_at,
		export_jobs.finished_at, export_jobs.expires_at
	FROM export_jobs
`

type scanner interface {
	Scan(...any) error
}

func scanExport(row scanner) (domain.Export, error) {
	var export domain.Export
	err := row.Scan(
		&export.ID,
		&export.TreeID,
		&export.ClientRequestID,
		&export.RequestedBy,
		&export.Format,
		&export.SchemaVersion,
		&export.Parameters,
		&export.Status,
		&export.Progress,
		&export.ResultObjectKey,
		&export.ResultMIMEType,
		&export.ResultSizeBytes,
		&export.ResultChecksumSHA256,
		&export.ErrorCode,
		&export.CreatedAt,
		&export.UpdatedAt,
		&export.StartedAt,
		&export.FinishedAt,
		&export.ExpiresAt,
	)
	return export, err
}

func sameJSON(left json.RawMessage, right json.RawMessage) bool {
	var leftValue any
	var rightValue any
	leftDecoder := json.NewDecoder(bytes.NewReader(left))
	leftDecoder.UseNumber()
	rightDecoder := json.NewDecoder(bytes.NewReader(right))
	rightDecoder.UseNumber()
	if leftDecoder.Decode(&leftValue) != nil || rightDecoder.Decode(&rightValue) != nil {
		return bytes.Equal(left, right)
	}
	leftCanonical, _ := json.Marshal(leftValue)
	rightCanonical, _ := json.Marshal(rightValue)
	return bytes.Equal(leftCanonical, rightCanonical)
}
