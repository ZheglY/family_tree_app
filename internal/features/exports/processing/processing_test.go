package processing

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/jobs"
	"github.com/ZheglY/family_tree_app/internal/core/storage"
	"github.com/ZheglY/family_tree_app/internal/features/exports/domain"
	"github.com/ZheglY/family_tree_app/internal/features/exports/exportjob"
	"github.com/ZheglY/family_tree_app/internal/features/exports/manifest"
	"github.com/google/uuid"
)

type processingRepositoryStub struct {
	export     domain.Export
	snapshot   manifest.Snapshot
	completed  bool
	failedCode string
	candidates []domain.CleanupCandidate
	cleared    []domain.CleanupCandidate
	loadErr    error
}

func (stub *processingRepositoryStub) AcquireForGeneration(
	_ context.Context, _ uuid.UUID, _ uuid.UUID, now time.Time,
) (domain.Export, error) {
	if stub.export.ID == uuid.Nil {
		return domain.Export{}, domain.ErrExportNotFound
	}
	if stub.export.Status == domain.StatusQueued {
		stub.export.Status = domain.StatusRunning
		stub.export.StartedAt = &now
	}
	return stub.export, nil
}

func (stub *processingRepositoryStub) LoadManifest(context.Context, domain.Export) (manifest.Snapshot, error) {
	return stub.snapshot, stub.loadErr
}

func (stub *processingRepositoryStub) MarkCompleted(
	_ context.Context, _ domain.Export, key string, mime string, size int64, checksum string,
	expiresAt time.Time, now time.Time,
) error {
	stub.completed = true
	stub.export.Status = domain.StatusCompleted
	stub.export.ResultObjectKey = key
	stub.export.ResultMIMEType = mime
	stub.export.ResultSizeBytes = size
	stub.export.ResultChecksumSHA256 = checksum
	stub.export.ExpiresAt = &expiresAt
	stub.export.FinishedAt = &now
	return nil
}

func (stub *processingRepositoryStub) MarkFailed(
	_ context.Context, _ domain.Export, code string, _ time.Time,
) error {
	stub.failedCode = code
	return nil
}

func (stub *processingRepositoryStub) ReserveCleanupCandidates(
	context.Context, time.Time, int, time.Time,
) ([]domain.CleanupCandidate, error) {
	return stub.candidates, nil
}

func (stub *processingRepositoryStub) GetDeletionCandidate(
	_ context.Context, treeID uuid.UUID, exportID uuid.UUID,
) (domain.CleanupCandidate, error) {
	for _, candidate := range stub.candidates {
		if candidate.TreeID == treeID && candidate.ExportID == exportID {
			return candidate, nil
		}
	}
	return domain.CleanupCandidate{}, domain.ErrExportNotFound
}

func (stub *processingRepositoryStub) ClearExpiredResult(
	_ context.Context, candidate domain.CleanupCandidate, _ time.Time,
) error {
	stub.cleared = append(stub.cleared, candidate)
	return nil
}

type processorObjectStoreStub struct {
	objects map[string]storage.PutInput
	deleted []string
	putErr  error
}

func (stub *processorObjectStoreStub) DownloadObject(
	_ context.Context,
	key string,
	maxBytes int64,
) ([]byte, storage.ObjectInfo, error) {
	object, exists := stub.objects[key]
	if !exists {
		return nil, storage.ObjectInfo{}, storage.ErrObjectNotFound
	}
	if int64(len(object.Body)) > maxBytes {
		return nil, storage.ObjectInfo{}, errors.New("object exceeds limit")
	}
	return object.Body, storage.ObjectInfo{
		ContentType: object.ContentType, SizeBytes: int64(len(object.Body)),
		ChecksumSHA256: object.ChecksumSHA256,
	}, nil
}

func (stub *processorObjectStoreStub) PutObject(_ context.Context, input storage.PutInput) (storage.ObjectInfo, error) {
	if stub.putErr != nil {
		return storage.ObjectInfo{}, stub.putErr
	}
	stub.objects[input.ObjectKey] = input
	return storage.ObjectInfo{
		ContentType: input.ContentType, SizeBytes: int64(len(input.Body)), ChecksumSHA256: input.ChecksumSHA256,
	}, nil
}

func (stub *processorObjectStoreStub) DeleteObject(_ context.Context, key string) error {
	stub.deleted = append(stub.deleted, key)
	delete(stub.objects, key)
	return nil
}

func TestGeneratorCreatesVersionedManifest(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	export, err := domain.New(uuid.New(), uuid.New(), uuid.New(), domain.CreateValues{
		ClientRequestID: uuid.New(), Format: domain.FormatJSONBackup,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	repository := &processingRepositoryStub{
		export: export,
		snapshot: manifest.Snapshot{Manifest: manifest.Manifest{
			Schema:  manifest.Schema{Name: domain.ManifestSchemaName, Version: domain.ManifestSchemaVersion},
			Export:  manifest.ExportMetadata{ID: export.ID, Format: export.Format},
			Tree:    manifest.Tree{ID: export.TreeID, Name: "Род Волконских"},
			Members: []manifest.TreeMember{}, Persons: []manifest.Person{}, PersonNames: []manifest.PersonName{},
			ParentChildRelations: []manifest.ParentChildRelation{}, Unions: []manifest.FamilyUnion{},
			UnionMembers: []manifest.UnionMember{}, MediaAssets: []manifest.MediaAsset{},
			MediaVariants: []manifest.MediaVariant{}, PersonMedia: []manifest.PersonMediaAttachment{},
		}},
	}
	objectStore := &processorObjectStoreStub{objects: make(map[string]storage.PutInput)}
	generator := NewGenerator(repository, objectStore, 24*time.Hour, 16*1024*1024, 250, 64*1024*1024)
	generator.now = func() time.Time { return now }
	payload, _ := exportjob.Encode(exportjob.GeneratePayload{TreeID: export.TreeID, ExportID: export.ID})
	if err := generator.Handle(context.Background(), jobs.Job{
		Kind: exportjob.KindGenerate, Payload: payload, Attempts: 1, MaxAttempts: 5,
	}); err != nil {
		t.Fatal(err)
	}
	var key string
	for storedKey := range objectStore.objects {
		key = storedKey
	}
	stored, exists := objectStore.objects[key]
	if !exists || !repository.completed || stored.ContentType != "application/json" {
		t.Fatalf("stored = %#v, completed = %t", stored, repository.completed)
	}
	var decoded manifest.Manifest
	if err := json.Unmarshal(stored.Body, &decoded); err != nil {
		t.Fatalf("decode generated manifest: %v", err)
	}
	if decoded.Schema.Name != domain.ManifestSchemaName || decoded.Schema.Version != 1 ||
		decoded.Tree.ID != export.TreeID || repository.export.ExpiresAt == nil ||
		!repository.export.ExpiresAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("generated manifest = %#v; export = %#v", decoded, repository.export)
	}
}

func TestGeneratorMarksPermanentFailure(t *testing.T) {
	t.Parallel()
	export, _ := domain.New(uuid.New(), uuid.New(), uuid.New(), domain.CreateValues{
		ClientRequestID: uuid.New(), Format: domain.FormatJSONBackup,
	}, time.Now().UTC())
	repository := &processingRepositoryStub{export: export, loadErr: errors.New("database unavailable")}
	objectStore := &processorObjectStoreStub{objects: make(map[string]storage.PutInput)}
	payload, _ := exportjob.Encode(exportjob.GeneratePayload{TreeID: export.TreeID, ExportID: export.ID})
	err := NewGenerator(repository, objectStore, time.Hour, 16*1024*1024, 250, 64*1024*1024).Handle(context.Background(), jobs.Job{
		Kind: exportjob.KindGenerate, Payload: payload, Attempts: 5, MaxAttempts: 5,
	})
	if err == nil || repository.failedCode != "generation_failed" {
		t.Fatalf("error = %v, failure code = %q", err, repository.failedCode)
	}
}

func TestGeneratorCreatesZIPBackupWithFilesAndChecksums(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	export, err := domain.New(uuid.New(), uuid.New(), uuid.New(), domain.CreateValues{
		ClientRequestID: uuid.New(), Format: domain.FormatZIPBackup,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	mediaID := uuid.New()
	sourceBody := []byte("private family document")
	sourceChecksum := sha256.Sum256(sourceBody)
	sourceChecksumHex := hex.EncodeToString(sourceChecksum[:])
	source := manifest.SourceFile{
		MediaID: mediaID, ArchivePath: "media/" + mediaID.String() + "/original",
		ObjectKey: "private-source", SizeBytes: int64(len(sourceBody)), ChecksumSHA256: sourceChecksumHex,
	}
	repository := &processingRepositoryStub{
		export: export,
		snapshot: manifest.Snapshot{
			Manifest: manifest.Manifest{
				Schema:  manifest.Schema{Name: domain.ManifestSchemaName, Version: 1},
				Export:  manifest.ExportMetadata{ID: export.ID, Format: export.Format},
				Tree:    manifest.Tree{ID: export.TreeID, Name: "Род Волконских"},
				Members: []manifest.TreeMember{}, Persons: []manifest.Person{}, PersonNames: []manifest.PersonName{},
				ParentChildRelations: []manifest.ParentChildRelation{}, Unions: []manifest.FamilyUnion{},
				UnionMembers: []manifest.UnionMember{},
				MediaAssets: []manifest.MediaAsset{{
					ID: mediaID, Status: "ready", SizeBytes: int64(len(sourceBody)), ChecksumSHA256: sourceChecksumHex,
				}},
				MediaVariants: []manifest.MediaVariant{}, PersonMedia: []manifest.PersonMediaAttachment{},
			},
			Files: []manifest.SourceFile{source},
		},
	}
	objectStore := &processorObjectStoreStub{objects: map[string]storage.PutInput{
		source.ObjectKey: {
			ObjectKey: source.ObjectKey, ContentType: "application/pdf",
			ChecksumSHA256: sourceChecksumHex, Body: sourceBody,
		},
	}}
	generator := NewGenerator(repository, objectStore, 24*time.Hour, 16*1024*1024, 250, 64*1024*1024)
	generator.now = func() time.Time { return now }
	payload, _ := exportjob.Encode(exportjob.GeneratePayload{TreeID: export.TreeID, ExportID: export.ID})
	if err := generator.Handle(context.Background(), jobs.Job{
		Kind: exportjob.KindGenerate, Payload: payload, Attempts: 1, MaxAttempts: 5,
	}); err != nil {
		t.Fatal(err)
	}
	var archiveBody []byte
	for key, object := range objectStore.objects {
		if strings.HasSuffix(key, ".zip") {
			archiveBody = object.Body
		}
	}
	if len(archiveBody) == 0 || repository.export.ResultMIMEType != archiveMIMEType {
		t.Fatalf("archive was not stored: export = %#v", repository.export)
	}
	reader, err := zip.NewReader(bytes.NewReader(archiveBody), int64(len(archiveBody)))
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string][]byte)
	for _, file := range reader.File {
		opened, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(opened)
		_ = opened.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries[file.Name] = body
	}
	if !bytes.Equal(entries[source.ArchivePath], sourceBody) ||
		!strings.Contains(string(entries["checksums.sha256"]), sourceChecksumHex+"  "+source.ArchivePath) {
		t.Fatalf("archive entries = %#v", entries)
	}
	var archivedManifest manifest.Manifest
	if err := json.Unmarshal(entries["manifest.json"], &archivedManifest); err != nil {
		t.Fatal(err)
	}
	if archivedManifest.MediaAssets[0].ArchivePath != source.ArchivePath {
		t.Fatalf("archived manifest = %#v", archivedManifest)
	}
}

func TestGeneratorRejectsOversizedZIPBeforeDownloadingFiles(t *testing.T) {
	t.Parallel()
	export, _ := domain.New(uuid.New(), uuid.New(), uuid.New(), domain.CreateValues{
		ClientRequestID: uuid.New(), Format: domain.FormatZIPBackup,
	}, time.Now().UTC())
	repository := &processingRepositoryStub{
		export: export,
		snapshot: manifest.Snapshot{
			Manifest: manifest.Manifest{
				Schema:  manifest.Schema{Name: domain.ManifestSchemaName, Version: 1},
				Export:  manifest.ExportMetadata{ID: export.ID, Format: export.Format},
				Tree:    manifest.Tree{ID: export.TreeID},
				Members: []manifest.TreeMember{}, Persons: []manifest.Person{}, PersonNames: []manifest.PersonName{},
				ParentChildRelations: []manifest.ParentChildRelation{}, Unions: []manifest.FamilyUnion{},
				UnionMembers: []manifest.UnionMember{}, MediaAssets: []manifest.MediaAsset{},
				MediaVariants: []manifest.MediaVariant{}, PersonMedia: []manifest.PersonMediaAttachment{},
			},
			Files: []manifest.SourceFile{{
				MediaID: uuid.New(), ArchivePath: "media/id/original", ObjectKey: "too-large",
				SizeBytes: 2 * 1024 * 1024, ChecksumSHA256: strings.Repeat("a", 64),
			}},
		},
	}
	objectStore := &processorObjectStoreStub{objects: make(map[string]storage.PutInput)}
	payload, _ := exportjob.Encode(exportjob.GeneratePayload{TreeID: export.TreeID, ExportID: export.ID})
	err := NewGenerator(repository, objectStore, time.Hour, 1024*1024, 250, 64*1024*1024).Handle(
		context.Background(),
		jobs.Job{Kind: exportjob.KindGenerate, Payload: payload, Attempts: 1, MaxAttempts: 5},
	)
	if err != nil || repository.failedCode != "archive_too_large" || len(objectStore.objects) != 0 {
		t.Fatalf("error = %v, failure code = %q, objects = %#v", err, repository.failedCode, objectStore.objects)
	}
}

func TestGeneratorCreatesVisualFormats(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		format string
		mime   string
		suffix string
		magic  []byte
	}{
		{format: domain.FormatSVG, mime: "image/svg+xml", suffix: ".svg", magic: []byte("<svg")},
		{format: domain.FormatPNG, mime: "image/png", suffix: ".png", magic: []byte("\x89PNG")},
		{format: domain.FormatPDF, mime: "application/pdf", suffix: ".pdf", magic: []byte("%PDF-")},
	} {
		t.Run(testCase.format, func(t *testing.T) {
			now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
			export, err := domain.New(uuid.New(), uuid.New(), uuid.New(), domain.CreateValues{
				ClientRequestID: uuid.New(), Format: testCase.format,
			}, now)
			if err != nil {
				t.Fatal(err)
			}
			repository := &processingRepositoryStub{export: export, snapshot: testVisualSnapshot(export, now)}
			objectStore := &processorObjectStoreStub{objects: make(map[string]storage.PutInput)}
			generator := NewGenerator(repository, objectStore, 24*time.Hour, 16*1024*1024, 20, 64*1024*1024)
			generator.now = func() time.Time { return now }
			payload, _ := exportjob.Encode(exportjob.GeneratePayload{TreeID: export.TreeID, ExportID: export.ID})
			if err := generator.Handle(context.Background(), jobs.Job{
				Kind: exportjob.KindGenerate, Payload: payload, Attempts: 1, MaxAttempts: 5,
			}); err != nil {
				t.Fatal(err)
			}
			stored, exists := objectStore.objects[repository.export.ResultObjectKey]
			if !exists || !repository.completed || stored.ContentType != testCase.mime ||
				!strings.HasSuffix(stored.ObjectKey, testCase.suffix) || !bytes.HasPrefix(stored.Body, testCase.magic) {
				t.Fatalf("stored visual = %#v, export = %#v", stored, repository.export)
			}
		})
	}
}

func TestGeneratorRejectsOversizedVisualTree(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	export, _ := domain.New(uuid.New(), uuid.New(), uuid.New(), domain.CreateValues{
		ClientRequestID: uuid.New(), Format: domain.FormatSVG,
	}, now)
	repository := &processingRepositoryStub{export: export, snapshot: testVisualSnapshot(export, now)}
	objectStore := &processorObjectStoreStub{objects: make(map[string]storage.PutInput)}
	payload, _ := exportjob.Encode(exportjob.GeneratePayload{TreeID: export.TreeID, ExportID: export.ID})
	err := NewGenerator(repository, objectStore, time.Hour, 16*1024*1024, 1, 64*1024*1024).Handle(
		context.Background(),
		jobs.Job{Kind: exportjob.KindGenerate, Payload: payload, Attempts: 1, MaxAttempts: 5},
	)
	if err != nil || repository.failedCode != "visual_too_large" || len(objectStore.objects) != 0 {
		t.Fatalf("error = %v, failure code = %q, objects = %#v", err, repository.failedCode, objectStore.objects)
	}
}

func testVisualSnapshot(export domain.Export, now time.Time) manifest.Snapshot {
	parentID, childID := uuid.New(), uuid.New()
	return manifest.Snapshot{Manifest: manifest.Manifest{
		Schema: manifest.Schema{Name: domain.ManifestSchemaName, Version: domain.ManifestSchemaVersion},
		Export: manifest.ExportMetadata{
			ID: export.ID, Format: export.Format, RequestedBy: export.RequestedBy, CreatedAt: now,
		},
		Tree: manifest.Tree{ID: export.TreeID, Name: "Род Волконских"},
		Persons: []manifest.Person{
			{ID: parentID, Sex: "female", LifeStatus: "deceased"},
			{ID: childID, Sex: "male", LifeStatus: "alive"},
		},
		PersonNames: []manifest.PersonName{
			{ID: uuid.New(), PersonID: parentID, FullText: "Анна Волконская", IsPreferred: true},
			{ID: uuid.New(), PersonID: childID, FullText: "Пётр Волконский", IsPreferred: true},
		},
		ParentChildRelations: []manifest.ParentChildRelation{
			{ID: uuid.New(), ParentPersonID: parentID, ChildPersonID: childID},
		},
	}}
}

func TestCleanupAndDeletionRemovePrivateResult(t *testing.T) {
	t.Parallel()
	treeID, exportID := uuid.New(), uuid.New()
	key := ResultObjectKey(treeID, exportID, domain.FormatJSONBackup, "checksum")
	candidate := domain.CleanupCandidate{TreeID: treeID, ExportID: exportID, ResultObjectKey: key}
	repository := &processingRepositoryStub{candidates: []domain.CleanupCandidate{candidate}}
	objectStore := &processorObjectStoreStub{objects: map[string]storage.PutInput{key: {ObjectKey: key}}}
	payload, _ := exportjob.Encode(exportjob.CleanupPayload{ExpiredBefore: time.Now().UTC(), BatchSize: 10})
	if err := NewCleanup(repository, objectStore).Handle(context.Background(), jobs.Job{
		Kind: exportjob.KindCleanup, Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
	if len(repository.cleared) != 1 || len(objectStore.deleted) != 1 {
		t.Fatalf("cleared = %#v, deleted = %#v", repository.cleared, objectStore.deleted)
	}

	repository.cleared = nil
	objectStore.deleted = nil
	deletePayload, _ := exportjob.Encode(exportjob.DeletePayload{TreeID: treeID, ExportID: exportID})
	if err := NewDeleter(repository, objectStore).Handle(context.Background(), jobs.Job{
		Kind: exportjob.KindDelete, Payload: deletePayload,
	}); err != nil {
		t.Fatal(err)
	}
	if len(repository.cleared) != 1 || len(objectStore.deleted) != 1 {
		t.Fatalf("manual clear = %#v, deleted = %#v", repository.cleared, objectStore.deleted)
	}
}

var _ storage.ProcessorObjectStore = (*processorObjectStoreStub)(nil)
