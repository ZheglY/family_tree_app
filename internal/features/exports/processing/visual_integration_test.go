package processing_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/jobs"
	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/core/storage"
	s3storage "github.com/ZheglY/family_tree_app/internal/core/storage/s3"
	"github.com/ZheglY/family_tree_app/internal/features/exports/domain"
	"github.com/ZheglY/family_tree_app/internal/features/exports/exportjob"
	"github.com/ZheglY/family_tree_app/internal/features/exports/processing"
	exportpostgres "github.com/ZheglY/family_tree_app/internal/features/exports/repository/postgres"
	"github.com/ZheglY/family_tree_app/internal/features/exports/service"
	"github.com/ZheglY/family_tree_app/internal/testdatabase"
	"github.com/ZheglY/family_tree_app/migrations"
	"github.com/google/uuid"
)

func TestVisualGEDCOMAndGEDZIPGeneratorsStoreResultsInPostgresAndS3(t *testing.T) {
	databaseURL := os.Getenv("FAMILY_TEST_DATABASE_URL")
	endpoint := os.Getenv("S3_TEST_ENDPOINT")
	if databaseURL == "" || endpoint == "" {
		t.Skip("FAMILY_TEST_DATABASE_URL and S3_TEST_ENDPOINT are not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	database, err := testdatabase.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	}()
	runner, err := migrations.NewRunner(database.Pool, logger.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	objectStore, err := s3storage.New(ctx, s3storage.Config{
		Endpoint:        endpoint,
		Region:          visualEnvOrDefault("S3_TEST_REGION", "ru-central1"),
		Bucket:          visualEnvOrDefault("S3_TEST_BUCKET", "family-tree-media-test"),
		AccessKeyID:     visualEnvOrDefault("S3_TEST_ACCESS_KEY_ID", "family-tree"),
		SecretAccessKey: visualEnvOrDefault("S3_TEST_SECRET_ACCESS_KEY", "family-tree-secret"),
		UsePathStyle:    true,
		RequestTimeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := objectStore.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	treeID, ownerID := uuid.New(), uuid.New()
	parentID, childID := uuid.New(), uuid.New()
	mediaID := uuid.New()
	mediaBody := []byte("private GEDZIP family portrait")
	mediaDigest := sha256.Sum256(mediaBody)
	mediaChecksum := hex.EncodeToString(mediaDigest[:])
	mediaObjectKey := "integration/gedzip/" + mediaID.String() + "/original.jpg"
	if _, err := objectStore.PutObject(ctx, storage.PutInput{
		ObjectKey: mediaObjectKey, ContentType: "image/jpeg",
		ChecksumSHA256: mediaChecksum, Body: mediaBody,
	}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := objectStore.DeleteObject(context.Background(), mediaObjectKey); err != nil {
			t.Errorf("delete GEDZIP source object: %v", err)
		}
	}()
	seed := []struct {
		query     string
		arguments []any
	}{
		{query: `
			INSERT INTO family_trees (
				id, name, owner_user_id, privacy, locale, timezone, created_at, updated_at
			) VALUES ($1, 'Род Волконских', $2, 'private', 'ru-RU', 'Europe/Moscow', $3, $3)
		`, arguments: []any{treeID, ownerID, now}},
		{query: `
			INSERT INTO tree_members (
				tree_id, user_id, role, status, created_at, accepted_at
			) VALUES ($1, $2, 'owner', 'active', $3, $3)
		`, arguments: []any{treeID, ownerID, now}},
		{query: `
			INSERT INTO persons (
				id, tree_id, sex, life_status, privacy_level,
				created_by, updated_by, created_at, updated_at
			) VALUES
				($1, $3, 'female', 'deceased', 'tree_members', $4, $4, $5, $5),
				($2, $3, 'male', 'alive', 'tree_members', $4, $4, $5, $5)
		`, arguments: []any{parentID, childID, treeID, ownerID, now}},
		{query: `
			INSERT INTO person_names (
				id, person_id, tree_id, type, full_text, is_preferred,
				language_code, created_at, updated_at
			) VALUES
				($1, $3, $5, 'primary', 'Анна Волконская', true, 'ru-RU', $6, $6),
				($2, $4, $5, 'primary', 'Пётр Волконский', true, 'ru-RU', $6, $6)
		`, arguments: []any{uuid.New(), uuid.New(), parentID, childID, treeID, now}},
		{query: `
			INSERT INTO parent_child_relations (
				id, tree_id, parent_person_id, child_person_id, relation_type,
				confidence, created_by, created_at, updated_at
			) VALUES ($1, $2, $3, $4, 'biological', 'confirmed', $5, $6, $6)
		`, arguments: []any{uuid.New(), treeID, parentID, childID, ownerID, now}},
		{query: `
			INSERT INTO media_assets (
				id, tree_id, client_request_id, kind, status, object_key,
				original_filename, mime_type, size_bytes, checksum_sha256,
				uploaded_by, uploaded_at, created_at, updated_at, processed_at
			) VALUES (
				$1, $2, $3, 'photo', 'ready', $4,
				'portrait.jpg', 'image/jpeg', $5, $6,
				$7, $8, $8, $8, $8
			)
		`, arguments: []any{mediaID, treeID, uuid.New(), mediaObjectKey, int64(len(mediaBody)), mediaChecksum, ownerID, now}},
		{query: `
			INSERT INTO person_media (
				tree_id, person_id, media_id, role, sort_order, created_by, created_at
			) VALUES ($1, $2, $3, 'profile', 0, $4, $5)
		`, arguments: []any{treeID, childID, mediaID, ownerID, now}},
		{query: `
			UPDATE persons SET primary_media_id = $3 WHERE tree_id = $1 AND id = $2
		`, arguments: []any{treeID, childID, mediaID}},
	}
	for _, statement := range seed {
		if _, err := database.Pool.Exec(ctx, statement.query, statement.arguments...); err != nil {
			t.Fatalf("seed visual export tree: %v", err)
		}
	}
	exportValue, err := domain.New(uuid.New(), treeID, ownerID, domain.CreateValues{
		ClientRequestID: uuid.New(), Format: domain.FormatPDF,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	repository := exportpostgres.New(database.Pool)
	exportValue, created, err := repository.CreateAccessible(ctx, exportValue, service.AuditContext{
		AuditID: uuid.New(), RequestID: "visual-pipeline-test", IPAddress: "127.0.0.1",
	})
	if err != nil || !created {
		t.Fatalf("create visual export = %#v, created = %t, error = %v", exportValue, created, err)
	}
	payload, _ := exportjob.Encode(exportjob.GeneratePayload{TreeID: treeID, ExportID: exportValue.ID})
	generator := processing.NewGenerator(repository, objectStore, time.Hour, 16*1024*1024, 20, 64*1024*1024)
	if err := generator.Handle(ctx, jobs.Job{
		Kind: exportjob.KindGenerate, Payload: payload, Attempts: 1, MaxAttempts: 5,
	}); err != nil {
		t.Fatal(err)
	}
	var status, objectKey, mimeType, checksum string
	var sizeBytes int64
	if err := database.Pool.QueryRow(ctx, `
		SELECT status, result_object_key, result_mime_type, result_size_bytes,
			result_checksum_sha256
		FROM export_jobs WHERE tree_id = $1 AND id = $2
	`, treeID, exportValue.ID).Scan(&status, &objectKey, &mimeType, &sizeBytes, &checksum); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if objectKey != "" {
			if err := objectStore.DeleteObject(context.Background(), objectKey); err != nil {
				t.Errorf("delete visual export object: %v", err)
			}
		}
	}()
	if status != domain.StatusCompleted || mimeType != "application/pdf" || sizeBytes < 100 {
		t.Fatalf("completed visual export = status %q, mime %q, size %d", status, mimeType, sizeBytes)
	}
	body, info, err := objectStore.DownloadObject(ctx, objectKey, 16*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	if !bytes.HasPrefix(body, []byte("%PDF-")) || int64(len(body)) != sizeBytes ||
		hex.EncodeToString(digest[:]) != checksum || info.ChecksumSHA256 != checksum {
		t.Fatal("stored PDF differs from completed export metadata")
	}

	gedcomExport, err := domain.New(uuid.New(), treeID, ownerID, domain.CreateValues{
		ClientRequestID: uuid.New(), Format: domain.FormatGEDCOM,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	gedcomExport, created, err = repository.CreateAccessible(ctx, gedcomExport, service.AuditContext{
		AuditID: uuid.New(), RequestID: "gedcom-pipeline-test", IPAddress: "127.0.0.1",
	})
	if err != nil || !created {
		t.Fatalf("create GEDCOM export = %#v, created = %t, error = %v", gedcomExport, created, err)
	}
	gedcomPayload, _ := exportjob.Encode(exportjob.GeneratePayload{TreeID: treeID, ExportID: gedcomExport.ID})
	if err := generator.Handle(ctx, jobs.Job{
		Kind: exportjob.KindGenerate, Payload: gedcomPayload, Attempts: 1, MaxAttempts: 5,
	}); err != nil {
		t.Fatal(err)
	}
	var gedcomStatus, gedcomKey, gedcomMIME, gedcomChecksum string
	var gedcomSize int64
	if err := database.Pool.QueryRow(ctx, `
		SELECT status, result_object_key, result_mime_type, result_size_bytes,
			result_checksum_sha256
		FROM export_jobs WHERE tree_id = $1 AND id = $2
	`, treeID, gedcomExport.ID).Scan(
		&gedcomStatus, &gedcomKey, &gedcomMIME, &gedcomSize, &gedcomChecksum,
	); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if gedcomKey != "" {
			if err := objectStore.DeleteObject(context.Background(), gedcomKey); err != nil {
				t.Errorf("delete GEDCOM export object: %v", err)
			}
		}
	}()
	gedcomBody, gedcomInfo, err := objectStore.DownloadObject(ctx, gedcomKey, 16*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	gedcomDigest := sha256.Sum256(gedcomBody)
	if gedcomStatus != domain.StatusCompleted || gedcomMIME != "text/vnd.familysearch.gedcom" ||
		!bytes.HasPrefix(gedcomBody, []byte{0xEF, 0xBB, 0xBF}) ||
		!bytes.Contains(gedcomBody, []byte("2 VERS 7.0\r\n")) ||
		!bytes.Contains(gedcomBody, []byte(" FAM\r\n")) ||
		!bytes.HasSuffix(gedcomBody, []byte("0 TRLR\r\n")) ||
		int64(len(gedcomBody)) != gedcomSize ||
		hex.EncodeToString(gedcomDigest[:]) != gedcomChecksum ||
		gedcomInfo.ChecksumSHA256 != gedcomChecksum {
		t.Fatal("stored GEDCOM differs from completed export metadata")
	}

	gedzipExport, err := domain.New(uuid.New(), treeID, ownerID, domain.CreateValues{
		ClientRequestID: uuid.New(), Format: domain.FormatGEDZIP,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	gedzipExport, created, err = repository.CreateAccessible(ctx, gedzipExport, service.AuditContext{
		AuditID: uuid.New(), RequestID: "gedzip-pipeline-test", IPAddress: "127.0.0.1",
	})
	if err != nil || !created {
		t.Fatalf("create GEDZIP export = %#v, created = %t, error = %v", gedzipExport, created, err)
	}
	gedzipPayload, _ := exportjob.Encode(exportjob.GeneratePayload{TreeID: treeID, ExportID: gedzipExport.ID})
	if err := generator.Handle(ctx, jobs.Job{
		Kind: exportjob.KindGenerate, Payload: gedzipPayload, Attempts: 1, MaxAttempts: 5,
	}); err != nil {
		t.Fatal(err)
	}
	var gedzipStatus, gedzipKey, gedzipMIME, gedzipChecksum string
	var gedzipSize int64
	if err := database.Pool.QueryRow(ctx, `
		SELECT status, result_object_key, result_mime_type, result_size_bytes,
			result_checksum_sha256
		FROM export_jobs WHERE tree_id = $1 AND id = $2
	`, treeID, gedzipExport.ID).Scan(
		&gedzipStatus, &gedzipKey, &gedzipMIME, &gedzipSize, &gedzipChecksum,
	); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if gedzipKey != "" {
			if err := objectStore.DeleteObject(context.Background(), gedzipKey); err != nil {
				t.Errorf("delete GEDZIP export object: %v", err)
			}
		}
	}()
	gedzipBody, gedzipInfo, err := objectStore.DownloadObject(ctx, gedzipKey, 16*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	gedzipDigest := sha256.Sum256(gedzipBody)
	if gedzipStatus != domain.StatusCompleted ||
		gedzipMIME != "application/vnd.familysearch.gedcom+zip" ||
		int64(len(gedzipBody)) != gedzipSize ||
		hex.EncodeToString(gedzipDigest[:]) != gedzipChecksum ||
		gedzipInfo.ChecksumSHA256 != gedzipChecksum {
		t.Fatal("stored GEDZIP differs from completed export metadata")
	}
	reader, err := zip.NewReader(bytes.NewReader(gedzipBody), int64(len(gedzipBody)))
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string][]byte)
	for _, file := range reader.File {
		opened, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		entryBody, err := io.ReadAll(opened)
		_ = opened.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries[file.Name] = entryBody
	}
	mediaPath := "media/" + mediaID.String() + "/original.jpg"
	if len(entries) != 2 || !bytes.Equal(entries[mediaPath], mediaBody) ||
		!bytes.Contains(entries["gedcom.ged"], []byte("1 FILE "+mediaPath+"\r\n")) ||
		!bytes.Contains(entries["gedcom.ged"], []byte("1 OBJE @O"+strings.ToUpper(strings.ReplaceAll(mediaID.String(), "-", ""))+"@\r\n")) {
		t.Fatalf("invalid GEDZIP entries: %#v", entries)
	}
}

func visualEnvOrDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
