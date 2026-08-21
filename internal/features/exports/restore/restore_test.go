package restore

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/core/storage"
	s3storage "github.com/ZheglY/family_tree_app/internal/core/storage/s3"
	"github.com/ZheglY/family_tree_app/internal/features/exports/domain"
	"github.com/ZheglY/family_tree_app/internal/features/exports/manifest"
	"github.com/ZheglY/family_tree_app/internal/testdatabase"
	"github.com/ZheglY/family_tree_app/migrations"
	"github.com/google/uuid"
)

func TestParseZIPValidatesSchemaChecksumsAndPaths(t *testing.T) {
	t.Parallel()
	backup, original, variant := testBackup(t)
	body := buildTestZIP(t, backup, map[string][]byte{
		backup.MediaAssets[0].ArchivePath:   original,
		backup.MediaVariants[0].ArchivePath: variant,
	})
	parsed, err := ParseZIP(body, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Manifest.Tree.ID != backup.Tree.ID || len(parsed.Files) != 2 ||
		!bytes.Equal(parsed.Files[backup.MediaAssets[0].ArchivePath], original) {
		t.Fatalf("parsed archive = %#v", parsed)
	}

	unsafeBody := buildRawZIP(t, map[string][]byte{
		"../escape":        []byte("bad"),
		"manifest.json":    []byte("{}"),
		"checksums.sha256": []byte("bad"),
	})
	if _, err := ParseZIP(unsafeBody, 1024*1024); !errors.Is(err, ErrInvalidBackup) {
		t.Fatalf("unsafe ZIP error = %v", err)
	}

	tamperedManifest := backup
	tamperedManifest.MediaAssets = append([]manifest.MediaAsset(nil), backup.MediaAssets...)
	tamperedManifest.MediaAssets[0].ChecksumSHA256 = strings.Repeat("0", 64)
	tampered := buildTestZIP(t, tamperedManifest, map[string][]byte{
		backup.MediaAssets[0].ArchivePath:   original,
		backup.MediaVariants[0].ArchivePath: variant,
	})
	if _, err := ParseZIP(tampered, 1024*1024); !errors.Is(err, ErrInvalidBackup) {
		t.Fatal("tampered ZIP was accepted")
	}
	nonCanonical := backup
	nonCanonical.MediaAssets = append([]manifest.MediaAsset(nil), backup.MediaAssets...)
	nonCanonical.MediaAssets[0].ArchivePath = "media/renamed-original"
	nonCanonicalBody := buildTestZIP(t, nonCanonical, map[string][]byte{
		nonCanonical.MediaAssets[0].ArchivePath: original,
		backup.MediaVariants[0].ArchivePath:     variant,
	})
	if _, err := ParseZIP(nonCanonicalBody, 1024*1024); !errors.Is(err, ErrInvalidBackup) {
		t.Fatal("non-canonical archive path was accepted")
	}
	cyclic := backup
	cyclic.ParentChildRelations = append(
		append([]manifest.ParentChildRelation(nil), backup.ParentChildRelations...),
		manifest.ParentChildRelation{
			ID:             uuid.New(),
			ParentPersonID: backup.ParentChildRelations[0].ChildPersonID,
			ChildPersonID:  backup.ParentChildRelations[0].ParentPersonID,
			RelationType:   "biological",
			Confidence:     "confirmed",
			CreatedBy:      backup.Tree.OwnerUserID,
			CreatedAt:      backup.Tree.CreatedAt,
			UpdatedAt:      backup.Tree.UpdatedAt,
			Version:        1,
		},
	)
	cyclicBody := buildTestZIP(t, cyclic, map[string][]byte{
		backup.MediaAssets[0].ArchivePath:   original,
		backup.MediaVariants[0].ArchivePath: variant,
	})
	if _, err := ParseZIP(cyclicBody, 1024*1024); !errors.Is(err, ErrInvalidBackup) {
		t.Fatal("cyclic parent-child graph was accepted")
	}
	if _, err := ParseZIP(body, 10); !errors.Is(err, ErrBackupTooLarge) {
		t.Fatalf("oversized ZIP error = %v", err)
	}
}

func TestRestoredMediaStateSafelyResumesInFlightFiles(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		source       string
		wantStatus   string
		wantEnqueued bool
	}{
		{source: "pending", wantStatus: "rejected"},
		{source: "uploaded", wantStatus: "uploaded", wantEnqueued: true},
		{source: "processing", wantStatus: "uploaded", wantEnqueued: true},
		{source: "ready", wantStatus: "ready"},
		{source: "rejected", wantStatus: "rejected"},
		{source: "deleted", wantStatus: "deleted"},
	} {
		status, _, _, enqueued := restoredMediaState(manifest.MediaAsset{Status: testCase.source}, now)
		if status != testCase.wantStatus || enqueued != testCase.wantEnqueued {
			t.Fatalf(
				"restoredMediaState(%q) = (%q, %t), want (%q, %t)",
				testCase.source,
				status,
				enqueued,
				testCase.wantStatus,
				testCase.wantEnqueued,
			)
		}
	}
}

type restoreObjectStore struct {
	objects map[string]storage.PutInput
	deleted []string
}

func (store *restoreObjectStore) PutObjectIfAbsent(
	_ context.Context,
	input storage.PutInput,
) (storage.ObjectInfo, error) {
	if _, exists := store.objects[input.ObjectKey]; exists {
		return storage.ObjectInfo{}, storage.ErrObjectAlreadyExists
	}
	input.Body = append([]byte(nil), input.Body...)
	store.objects[input.ObjectKey] = input
	return storage.ObjectInfo{
		ContentType: input.ContentType, SizeBytes: int64(len(input.Body)),
		ChecksumSHA256: input.ChecksumSHA256, ETag: "restored-etag",
	}, nil
}

func (store *restoreObjectStore) DeleteObject(_ context.Context, key string) error {
	delete(store.objects, key)
	store.deleted = append(store.deleted, key)
	return nil
}

func TestRestorerRebuildsCleanDatabaseAndCompensatesFailure(t *testing.T) {
	databaseURL := os.Getenv("FAMILY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("FAMILY_TEST_DATABASE_URL is not configured")
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
	backup, original, variant := testBackup(t)
	body := buildTestZIP(t, backup, map[string][]byte{
		backup.MediaAssets[0].ArchivePath:   original,
		backup.MediaVariants[0].ArchivePath: variant,
	})
	parsed, err := ParseZIP(body, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	objectStore := &restoreObjectStore{objects: make(map[string]storage.PutInput)}
	result, err := NewRestorer(database.Pool, objectStore).Restore(ctx, parsed)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if result.TreeID != backup.Tree.ID || result.ObjectsRestored != 2 || result.JobsEnqueued != 0 {
		t.Fatalf("restore result = %#v", result)
	}
	for table, expected := range map[string]int{
		"family_trees": 1, "tree_members": 1, "persons": 2, "person_names": 2,
		"parent_child_relations": 1, "family_unions": 1, "union_members": 2,
		"media_assets": 1, "media_variants": 1, "person_media": 1,
	} {
		var count int
		if err := database.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != expected {
			t.Fatalf("%s count = %d, want %d", table, count, expected)
		}
	}
	var rootPersonID, coverMediaID, primaryMediaID uuid.UUID
	if err := database.Pool.QueryRow(ctx, `
		SELECT tree.root_person_id, tree.cover_media_id, person.primary_media_id
		FROM family_trees tree
		JOIN persons person ON person.tree_id = tree.id AND person.id = tree.root_person_id
		WHERE tree.id = $1
	`, backup.Tree.ID).Scan(&rootPersonID, &coverMediaID, &primaryMediaID); err != nil {
		t.Fatal(err)
	}
	if rootPersonID != backup.Persons[0].ID || coverMediaID != backup.MediaAssets[0].ID ||
		primaryMediaID != backup.MediaAssets[0].ID {
		t.Fatalf("restored pointers = %s %s %s", rootPersonID, coverMediaID, primaryMediaID)
	}
	var audits int
	if err := database.Pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log WHERE tree_id = $1 AND action = 'backup.restored'
	`, backup.Tree.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("restore audits = %d, error = %v", audits, err)
	}
	if len(objectStore.objects) != 2 {
		t.Fatalf("restored objects = %#v", objectStore.objects)
	}
	if _, err := NewRestorer(database.Pool, objectStore).Restore(ctx, parsed); !errors.Is(err, ErrRestoreConflict) {
		t.Fatalf("duplicate restore error = %v", err)
	}
	if len(objectStore.objects) != 2 {
		t.Fatalf("duplicate restore changed objects = %#v", objectStore.objects)
	}

	broken := parsed
	broken.Manifest.Tree.ID = uuid.New()
	broken.Manifest.MediaAssets[0].OriginalFilename = ""
	brokenStore := &restoreObjectStore{objects: make(map[string]storage.PutInput)}
	if _, err := NewRestorer(database.Pool, brokenStore).Restore(ctx, broken); err == nil {
		t.Fatal("invalid restore unexpectedly succeeded")
	}
	if len(brokenStore.objects) != 0 || len(brokenStore.deleted) != 2 {
		t.Fatalf("restore compensation = objects %#v, deleted %#v", brokenStore.objects, brokenStore.deleted)
	}
}

func TestRestorerRebuildsPostgresAndS3(t *testing.T) {
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
		Region:          restoreEnvOrDefault("S3_TEST_REGION", "ru-central1"),
		Bucket:          restoreEnvOrDefault("S3_TEST_BUCKET", "family-tree-media-test"),
		AccessKeyID:     restoreEnvOrDefault("S3_TEST_ACCESS_KEY_ID", "family-tree"),
		SecretAccessKey: restoreEnvOrDefault("S3_TEST_SECRET_ACCESS_KEY", "family-tree-secret"),
		UsePathStyle:    true,
		RequestTimeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := objectStore.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	backup, original, variant := testBackup(t)
	originalKey := originalObjectKey(backup.Tree.ID, backup.MediaAssets[0].ID)
	variantKey := variantObjectKey(
		backup.Tree.ID,
		backup.MediaVariants[0].MediaID,
		backup.MediaVariants[0].Kind,
	)
	defer func() {
		for _, key := range []string{variantKey, originalKey} {
			if err := objectStore.DeleteObject(context.Background(), key); err != nil {
				t.Errorf("delete restored object %s: %v", key, err)
			}
		}
	}()
	body := buildTestZIP(t, backup, map[string][]byte{
		backup.MediaAssets[0].ArchivePath:   original,
		backup.MediaVariants[0].ArchivePath: variant,
	})
	archive, err := ParseZIP(body, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewRestorer(database.Pool, objectStore).Restore(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	if result.TreeID != backup.Tree.ID || result.ObjectsRestored != 2 {
		t.Fatalf("restore result = %#v", result)
	}
	for key, expected := range map[string][]byte{
		originalKey: original,
		variantKey:  variant,
	} {
		downloaded, info, err := objectStore.DownloadObject(ctx, key, 1024*1024)
		if err != nil {
			t.Fatalf("download restored object %s: %v", key, err)
		}
		if !bytes.Equal(downloaded, expected) || info.ChecksumSHA256 != checksum(expected) {
			t.Fatalf("restored object %s differs from backup", key)
		}
	}
	var restored bool
	if err := database.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM audit_log
			WHERE tree_id = $1 AND action = 'backup.restored'
		)
	`, backup.Tree.ID).Scan(&restored); err != nil || !restored {
		t.Fatalf("restore audit exists = %v, error = %v", restored, err)
	}
}

func testBackup(t *testing.T) (manifest.Manifest, []byte, []byte) {
	t.Helper()
	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	treeID, ownerID := uuid.New(), uuid.New()
	parentID, childID := uuid.New(), uuid.New()
	mediaID, variantID := uuid.New(), uuid.New()
	unionID := uuid.New()
	original := []byte("private original family image")
	variant := []byte("private thumbnail")
	originalChecksum, variantChecksum := checksum(original), checksum(variant)
	acceptedAt, processedAt := now, now
	return manifest.Manifest{
		Schema: manifest.Schema{Name: domain.ManifestSchemaName, Version: domain.ManifestSchemaVersion},
		Export: manifest.ExportMetadata{
			ID: uuid.New(), Format: domain.FormatZIPBackup, RequestedBy: ownerID, CreatedAt: now,
		},
		Tree: manifest.Tree{
			ID: treeID, Name: "Род Волконских", OwnerUserID: ownerID,
			RootPersonID: &parentID, CoverMediaID: &mediaID, Privacy: "private",
			Locale: "ru-RU", Timezone: "Europe/Moscow", CreatedAt: now, UpdatedAt: now, Version: 1,
		},
		Members: []manifest.TreeMember{{
			UserID: ownerID, Role: "owner", Status: "active", CreatedAt: now, AcceptedAt: &acceptedAt,
		}},
		Persons: []manifest.Person{
			{ID: parentID, Sex: "female", LifeStatus: "deceased", Biography: "Архивная биография",
				PrimaryMediaID: &mediaID, PrivacyLevel: "tree_members", CreatedBy: ownerID,
				UpdatedBy: ownerID, CreatedAt: now, UpdatedAt: now, Version: 1},
			{ID: childID, Sex: "male", LifeStatus: "alive", PrivacyLevel: "tree_members",
				CreatedBy: ownerID, UpdatedBy: ownerID, CreatedAt: now, UpdatedAt: now, Version: 1},
		},
		PersonNames: []manifest.PersonName{
			{ID: uuid.New(), PersonID: parentID, Type: "primary", GivenName: "Анна",
				FullText: "Анна Волконская", IsPreferred: true, LanguageCode: "ru-RU", CreatedAt: now, UpdatedAt: now},
			{ID: uuid.New(), PersonID: childID, Type: "primary", GivenName: "Пётр",
				FullText: "Пётр Волконский", IsPreferred: true, LanguageCode: "ru-RU", CreatedAt: now, UpdatedAt: now},
		},
		ParentChildRelations: []manifest.ParentChildRelation{{
			ID: uuid.New(), ParentPersonID: parentID, ChildPersonID: childID,
			RelationType: "biological", Confidence: "confirmed", CreatedBy: ownerID,
			CreatedAt: now, UpdatedAt: now, Version: 1,
		}},
		Unions: []manifest.FamilyUnion{{
			ID: unionID, Type: "marriage", CreatedBy: ownerID, UpdatedBy: ownerID,
			CreatedAt: now, UpdatedAt: now, Version: 1,
		}},
		UnionMembers: []manifest.UnionMember{
			{UnionID: unionID, PersonID: parentID, Role: "partner", CreatedAt: now},
			{UnionID: unionID, PersonID: childID, Role: "partner", CreatedAt: now},
		},
		MediaAssets: []manifest.MediaAsset{{
			ID: mediaID, Kind: "photo", Status: "ready", OriginalFilename: "portrait.png",
			MIMEType: "image/png", SizeBytes: int64(len(original)), ChecksumSHA256: originalChecksum,
			Caption: "Портрет", UploadedBy: ownerID, UploadedAt: &now, CreatedAt: now,
			UpdatedAt: now, ProcessedAt: &processedAt, Version: 1,
			ArchivePath: "media/" + mediaID.String() + "/original",
		}},
		MediaVariants: []manifest.MediaVariant{{
			ID: variantID, MediaID: mediaID, Kind: "thumbnail", MIMEType: "image/jpeg",
			SizeBytes: int64(len(variant)), ChecksumSHA256: variantChecksum,
			Width: 100, Height: 100, CreatedAt: now,
			ArchivePath: "media/" + mediaID.String() + "/variants/thumbnail",
		}},
		PersonMedia: []manifest.PersonMediaAttachment{{
			PersonID: parentID, MediaID: mediaID, Role: "profile", CreatedBy: ownerID, CreatedAt: now,
		}},
	}, original, variant
}

func buildTestZIP(t *testing.T, backup manifest.Manifest, files map[string][]byte) []byte {
	t.Helper()
	manifestBody, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	manifestBody = append(manifestBody, '\n')
	entries := map[string][]byte{"manifest.json": manifestBody}
	for name, body := range files {
		entries[name] = body
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, checksum(entries[name])+"  "+name)
	}
	entries["checksums.sha256"] = []byte(strings.Join(lines, "\n") + "\n")
	return buildRawZIP(t, entries)
}

func buildRawZIP(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(entries[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func restoreEnvOrDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

var _ ObjectStore = (*restoreObjectStore)(nil)
