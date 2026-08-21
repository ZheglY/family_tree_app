package restore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/jobs"
	jobpostgres "github.com/ZheglY/family_tree_app/internal/core/jobs/postgres"
	"github.com/ZheglY/family_tree_app/internal/core/storage"
	"github.com/ZheglY/family_tree_app/internal/features/exports/manifest"
	"github.com/ZheglY/family_tree_app/internal/features/media/mediajob"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ObjectStore interface {
	PutObjectIfAbsent(context.Context, storage.PutInput) (storage.ObjectInfo, error)
	DeleteObject(context.Context, string) error
}

type Restorer struct {
	pool        *pgxpool.Pool
	objectStore ObjectStore
	now         func() time.Time
}

type Result struct {
	TreeID          uuid.UUID
	ObjectsRestored int
	JobsEnqueued    int
}

func NewRestorer(pool *pgxpool.Pool, objectStore ObjectStore) *Restorer {
	return &Restorer{
		pool: pool, objectStore: objectStore,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (restorer *Restorer) Restore(ctx context.Context, archive Archive) (Result, error) {
	if restorer.pool == nil || restorer.objectStore == nil || archive.Manifest.Tree.ID == uuid.Nil {
		return Result{}, ErrInvalidBackup
	}
	if err := validateArchive(archive); err != nil {
		return Result{}, err
	}
	tx, err := restorer.pool.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("begin backup restore: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := insertTree(ctx, tx, archive.Manifest.Tree); err != nil {
		if isUniqueViolation(err) {
			return Result{}, ErrRestoreConflict
		}
		return Result{}, err
	}
	uploadedKeys := make([]string, 0, len(archive.Files))
	mediaObjects, variantObjects, err := restorer.restoreObjects(ctx, archive, &uploadedKeys)
	if err != nil {
		_ = tx.Rollback(ctx)
		return Result{}, errors.Join(err, restorer.cleanupObjects(ctx, uploadedKeys))
	}
	jobsEnqueued, err := restoreRecords(
		ctx, tx, archive.Manifest, mediaObjects, variantObjects, restorer.now(),
	)
	if err != nil {
		_ = tx.Rollback(ctx)
		return Result{}, errors.Join(err, restorer.cleanupObjects(ctx, uploadedKeys))
	}
	if err := tx.Commit(ctx); err != nil {
		committed, checkErr := restorer.restoreCommitted(
			ctx,
			archive.Manifest.Tree.ID,
			archive.Manifest.Export.ID,
		)
		if checkErr == nil && committed {
			return Result{
				TreeID: archive.Manifest.Tree.ID, ObjectsRestored: len(uploadedKeys), JobsEnqueued: jobsEnqueued,
			}, nil
		}
		return Result{}, errors.Join(
			fmt.Errorf("commit backup restore: %w", err), checkErr,
			restorer.cleanupObjects(ctx, uploadedKeys),
		)
	}
	return Result{
		TreeID: archive.Manifest.Tree.ID, ObjectsRestored: len(uploadedKeys), JobsEnqueued: jobsEnqueued,
	}, nil
}

type restoredObject struct {
	objectKey string
	etag      string
}

func (restorer *Restorer) restoreObjects(
	ctx context.Context,
	archive Archive,
	uploadedKeys *[]string,
) (map[uuid.UUID]restoredObject, map[uuid.UUID]restoredObject, error) {
	mediaObjects := make(map[uuid.UUID]restoredObject)
	variantObjects := make(map[uuid.UUID]restoredObject)
	for _, asset := range archive.Manifest.MediaAssets {
		if asset.ArchivePath == "" {
			continue
		}
		body, exists := archive.Files[asset.ArchivePath]
		if !exists {
			return nil, nil, ErrInvalidBackup
		}
		objectKey := originalObjectKey(archive.Manifest.Tree.ID, asset.ID)
		info, err := restorer.putNewObject(ctx, objectKey, asset.MIMEType, asset.ChecksumSHA256, body)
		if err != nil {
			return nil, nil, err
		}
		*uploadedKeys = append(*uploadedKeys, objectKey)
		mediaObjects[asset.ID] = restoredObject{objectKey: objectKey, etag: info.ETag}
	}
	for _, variant := range archive.Manifest.MediaVariants {
		if variant.ArchivePath == "" {
			continue
		}
		body, exists := archive.Files[variant.ArchivePath]
		if !exists {
			return nil, nil, ErrInvalidBackup
		}
		objectKey := variantObjectKey(archive.Manifest.Tree.ID, variant.MediaID, variant.Kind)
		info, err := restorer.putNewObject(ctx, objectKey, variant.MIMEType, variant.ChecksumSHA256, body)
		if err != nil {
			return nil, nil, err
		}
		*uploadedKeys = append(*uploadedKeys, objectKey)
		variantObjects[variant.ID] = restoredObject{objectKey: objectKey, etag: info.ETag}
	}
	return mediaObjects, variantObjects, nil
}

func (restorer *Restorer) putNewObject(
	ctx context.Context,
	objectKey string,
	mimeType string,
	expectedChecksum string,
	body []byte,
) (storage.ObjectInfo, error) {
	info, err := restorer.objectStore.PutObjectIfAbsent(ctx, storage.PutInput{
		ObjectKey: objectKey, ContentType: mimeType, ChecksumSHA256: expectedChecksum, Body: body,
	})
	if errors.Is(err, storage.ErrObjectAlreadyExists) {
		return storage.ObjectInfo{}, ErrRestoreConflict
	}
	if err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("restore object %s: %w", objectKey, err)
	}
	return info, nil
}

func (restorer *Restorer) cleanupObjects(ctx context.Context, objectKeys []string) error {
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	var result error
	for index := len(objectKeys) - 1; index >= 0; index-- {
		if err := restorer.objectStore.DeleteObject(cleanupContext, objectKeys[index]); err != nil {
			result = errors.Join(result, fmt.Errorf("compensate restore object %s: %w", objectKeys[index], err))
		}
	}
	return result
}

func (restorer *Restorer) restoreCommitted(
	ctx context.Context,
	treeID uuid.UUID,
	exportID uuid.UUID,
) (bool, error) {
	var exists bool
	if err := restorer.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM audit_log
			WHERE tree_id = $1
			  AND action = 'backup.restored'
			  AND changes ->> 'source_export_id' = $2
		)
	`, treeID, exportID.String()).
		Scan(&exists); err != nil {
		return false, fmt.Errorf("check committed restore: %w", err)
	}
	return exists, nil
}

func restoreRecords(
	ctx context.Context,
	tx pgx.Tx,
	backup manifest.Manifest,
	mediaObjects map[uuid.UUID]restoredObject,
	variantObjects map[uuid.UUID]restoredObject,
	now time.Time,
) (int, error) {
	if err := insertMembers(ctx, tx, backup); err != nil {
		return 0, err
	}
	if err := insertPersons(ctx, tx, backup); err != nil {
		return 0, err
	}
	if err := insertRelations(ctx, tx, backup); err != nil {
		return 0, err
	}
	if err := insertUnions(ctx, tx, backup); err != nil {
		return 0, err
	}
	jobsEnqueued, err := insertMedia(ctx, tx, backup, mediaObjects, variantObjects, now)
	if err != nil {
		return 0, err
	}
	if err := restorePointers(ctx, tx, backup); err != nil {
		return 0, err
	}
	changes, _ := json.Marshal(map[string]any{
		"schema_version":   backup.Schema.Version,
		"source_export_id": backup.Export.ID,
		"restored_objects": len(mediaObjects) + len(variantObjects),
	})
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (
			id, tree_id, actor_user_id, action, entity_type, entity_id,
			request_id, changes, created_at
		) VALUES ($1, $2, $3, 'backup.restored', 'family_tree', $2, 'offline-restore', $4, $5)
	`, uuid.New(), backup.Tree.ID, backup.Tree.OwnerUserID, changes, now); err != nil {
		return 0, fmt.Errorf("insert restore audit: %w", err)
	}
	return jobsEnqueued, nil
}

func insertTree(ctx context.Context, tx pgx.Tx, tree manifest.Tree) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO family_trees (
			id, name, description, owner_user_id, root_person_id, cover_media_id,
			privacy, locale, timezone, created_at, updated_at, deleted_at, version
		) VALUES ($1, $2, $3, $4, NULL, NULL, $5, $6, $7, $8, $9, $10, $11)
	`, tree.ID, tree.Name, tree.Description, tree.OwnerUserID, tree.Privacy, tree.Locale,
		tree.Timezone, tree.CreatedAt, tree.UpdatedAt, tree.DeletedAt, tree.Version)
	if err != nil {
		return fmt.Errorf("restore family tree: %w", err)
	}
	return nil
}

func insertMembers(ctx context.Context, tx pgx.Tx, backup manifest.Manifest) error {
	for _, member := range backup.Members {
		if _, err := tx.Exec(ctx, `
			INSERT INTO tree_members (
				tree_id, user_id, role, status, invited_by, created_at, accepted_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, backup.Tree.ID, member.UserID, member.Role, member.Status, member.InvitedBy,
			member.CreatedAt, member.AcceptedAt); err != nil {
			return fmt.Errorf("restore tree member: %w", err)
		}
	}
	return nil
}

func insertPersons(ctx context.Context, tx pgx.Tx, backup manifest.Manifest) error {
	for _, person := range backup.Persons {
		if _, err := tx.Exec(ctx, `
			INSERT INTO persons (
				id, tree_id, sex, life_status, biography, notes, primary_media_id,
				privacy_level, created_by, updated_by, created_at, updated_at, deleted_at, version
			) VALUES ($1, $2, $3, $4, $5, $6, NULL, $7, $8, $9, $10, $11, $12, $13)
		`, person.ID, backup.Tree.ID, person.Sex, person.LifeStatus, person.Biography,
			person.Notes, person.PrivacyLevel, person.CreatedBy, person.UpdatedBy,
			person.CreatedAt, person.UpdatedAt, person.DeletedAt, person.Version); err != nil {
			return fmt.Errorf("restore person: %w", err)
		}
	}
	for _, name := range backup.PersonNames {
		if _, err := tx.Exec(ctx, `
			INSERT INTO person_names (
				id, person_id, tree_id, type, given_name, patronymic, family_name,
				prefix, suffix, full_text, is_preferred, language_code, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		`, name.ID, name.PersonID, backup.Tree.ID, name.Type, name.GivenName, name.Patronymic,
			name.FamilyName, name.Prefix, name.Suffix, name.FullText, name.IsPreferred,
			name.LanguageCode, name.CreatedAt, name.UpdatedAt); err != nil {
			return fmt.Errorf("restore person name: %w", err)
		}
	}
	return nil
}

func insertRelations(ctx context.Context, tx pgx.Tx, backup manifest.Manifest) error {
	for _, relation := range backup.ParentChildRelations {
		if _, err := tx.Exec(ctx, `
			INSERT INTO parent_child_relations (
				id, tree_id, parent_person_id, child_person_id, relation_type,
				confidence, note, created_by, created_at, updated_at, deleted_at, version
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		`, relation.ID, backup.Tree.ID, relation.ParentPersonID, relation.ChildPersonID,
			relation.RelationType, relation.Confidence, relation.Note, relation.CreatedBy,
			relation.CreatedAt, relation.UpdatedAt, relation.DeletedAt, relation.Version); err != nil {
			return fmt.Errorf("restore parent-child relation: %w", err)
		}
	}
	return nil
}

func insertUnions(ctx context.Context, tx pgx.Tx, backup manifest.Manifest) error {
	for _, union := range backup.Unions {
		if _, err := tx.Exec(ctx, `
			INSERT INTO family_unions (
				id, tree_id, type, end_reason, note, created_by, updated_by,
				created_at, updated_at, deleted_at, version
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		`, union.ID, backup.Tree.ID, union.Type, union.EndReason, union.Note,
			union.CreatedBy, union.UpdatedBy, union.CreatedAt, union.UpdatedAt,
			union.DeletedAt, union.Version); err != nil {
			return fmt.Errorf("restore family union: %w", err)
		}
	}
	for _, member := range backup.UnionMembers {
		if _, err := tx.Exec(ctx, `
			INSERT INTO union_members (union_id, person_id, tree_id, role, created_at)
			VALUES ($1, $2, $3, $4, $5)
		`, member.UnionID, member.PersonID, backup.Tree.ID, member.Role, member.CreatedAt); err != nil {
			return fmt.Errorf("restore union member: %w", err)
		}
	}
	return nil
}

func insertMedia(
	ctx context.Context,
	tx pgx.Tx,
	backup manifest.Manifest,
	mediaObjects map[uuid.UUID]restoredObject,
	variantObjects map[uuid.UUID]restoredObject,
	now time.Time,
) (int, error) {
	jobsEnqueued := 0
	for _, asset := range backup.MediaAssets {
		status, processingError, processedAt, enqueue := restoredMediaState(asset, now)
		object := mediaObjects[asset.ID]
		if object.objectKey == "" {
			object.objectKey = originalObjectKey(backup.Tree.ID, asset.ID)
		}
		clientRequestID := uuid.NewSHA1(backup.Export.ID, []byte("restore-media:"+asset.ID.String()))
		if _, err := tx.Exec(ctx, `
			INSERT INTO media_assets (
				id, tree_id, client_request_id, kind, status, object_key,
				original_filename, mime_type, size_bytes, checksum_sha256, etag,
				width, height, caption, description, uploaded_by, uploaded_at,
				created_at, updated_at, deleted_at, processing_error, processed_at, version
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				$7, $8, $9, $10, $11,
				$12, $13, $14, $15, $16, $17,
				$18, $19, $20, $21, $22, $23
			)
		`, asset.ID, backup.Tree.ID, clientRequestID, asset.Kind, status, object.objectKey,
			asset.OriginalFilename, asset.MIMEType, asset.SizeBytes, asset.ChecksumSHA256, object.etag,
			asset.Width, asset.Height, asset.Caption, asset.Description, asset.UploadedBy,
			asset.UploadedAt, asset.CreatedAt, asset.UpdatedAt, asset.DeletedAt,
			processingError, processedAt, asset.Version); err != nil {
			return 0, fmt.Errorf("restore media asset: %w", err)
		}
		if enqueue {
			payload, _ := mediajob.Encode(mediajob.ProcessPayload{TreeID: backup.Tree.ID, MediaID: asset.ID})
			if _, _, err := jobpostgres.EnqueueWith(ctx, tx, jobs.EnqueueRequest{
				ID:   uuid.NewSHA1(backup.Export.ID, []byte("restore-job:"+asset.ID.String())),
				Kind: mediajob.KindProcess, DeduplicationKey: asset.ID.String(), Payload: payload,
				MaxAttempts: 5, AvailableAt: now, CreatedAt: now,
			}); err != nil {
				return 0, fmt.Errorf("enqueue restored media processing: %w", err)
			}
			jobsEnqueued++
		}
	}
	for _, variant := range backup.MediaVariants {
		object := variantObjects[variant.ID]
		if object.objectKey == "" {
			object.objectKey = variantObjectKey(backup.Tree.ID, variant.MediaID, variant.Kind)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO media_variants (
				id, tree_id, media_id, kind, object_key, mime_type,
				size_bytes, checksum_sha256, width, height, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		`, variant.ID, backup.Tree.ID, variant.MediaID, variant.Kind, object.objectKey,
			variant.MIMEType, variant.SizeBytes, variant.ChecksumSHA256,
			variant.Width, variant.Height, variant.CreatedAt); err != nil {
			return 0, fmt.Errorf("restore media variant: %w", err)
		}
	}
	for _, attachment := range backup.PersonMedia {
		if _, err := tx.Exec(ctx, `
			INSERT INTO person_media (
				tree_id, person_id, media_id, role, sort_order, created_by, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, backup.Tree.ID, attachment.PersonID, attachment.MediaID, attachment.Role,
			attachment.SortOrder, attachment.CreatedBy, attachment.CreatedAt); err != nil {
			return 0, fmt.Errorf("restore person media attachment: %w", err)
		}
	}
	return jobsEnqueued, nil
}

func restorePointers(ctx context.Context, tx pgx.Tx, backup manifest.Manifest) error {
	for _, person := range backup.Persons {
		if person.PrimaryMediaID == nil {
			continue
		}
		if _, err := tx.Exec(ctx, `
			UPDATE persons SET primary_media_id = $3 WHERE tree_id = $1 AND id = $2
		`, backup.Tree.ID, person.ID, person.PrimaryMediaID); err != nil {
			return fmt.Errorf("restore primary media pointer: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE family_trees SET root_person_id = $2, cover_media_id = $3 WHERE id = $1
	`, backup.Tree.ID, backup.Tree.RootPersonID, backup.Tree.CoverMediaID); err != nil {
		return fmt.Errorf("restore tree pointers: %w", err)
	}
	return nil
}

func restoredMediaState(asset manifest.MediaAsset, now time.Time) (string, string, *time.Time, bool) {
	switch asset.Status {
	case "pending":
		return "rejected", "incomplete upload was not restored", &now, false
	case "processing":
		return "uploaded", "", nil, true
	case "uploaded":
		return "uploaded", "", nil, true
	case "rejected":
		processedAt := asset.ProcessedAt
		if processedAt == nil {
			processedAt = &now
		}
		return "rejected", "rejected media restored without processing details", processedAt, false
	default:
		return asset.Status, "", asset.ProcessedAt, false
	}
}

func originalObjectKey(treeID uuid.UUID, mediaID uuid.UUID) string {
	return fmt.Sprintf("trees/%s/media/%s/original", treeID, mediaID)
}

func variantObjectKey(treeID uuid.UUID, mediaID uuid.UUID, kind string) string {
	return fmt.Sprintf("trees/%s/media/%s/variants/%s.jpg", treeID, mediaID, kind)
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}
