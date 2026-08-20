package processing

import (
	"context"
	"encoding/json"
	"errors"
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
	manifest   manifest.Manifest
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

func (stub *processingRepositoryStub) LoadManifest(context.Context, domain.Export) (manifest.Manifest, error) {
	return stub.manifest, stub.loadErr
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

func (stub *processorObjectStoreStub) DownloadObject(context.Context, string, int64) ([]byte, storage.ObjectInfo, error) {
	return nil, storage.ObjectInfo{}, errors.New("not implemented")
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
		manifest: manifest.Manifest{
			Schema:  manifest.Schema{Name: domain.ManifestSchemaName, Version: domain.ManifestSchemaVersion},
			Export:  manifest.ExportMetadata{ID: export.ID, Format: export.Format},
			Tree:    manifest.Tree{ID: export.TreeID, Name: "Род Волконских"},
			Members: []manifest.TreeMember{}, Persons: []manifest.Person{}, PersonNames: []manifest.PersonName{},
			ParentChildRelations: []manifest.ParentChildRelation{}, Unions: []manifest.FamilyUnion{},
			UnionMembers: []manifest.UnionMember{}, MediaAssets: []manifest.MediaAsset{},
			MediaVariants: []manifest.MediaVariant{}, PersonMedia: []manifest.PersonMediaAttachment{},
		},
	}
	objectStore := &processorObjectStoreStub{objects: make(map[string]storage.PutInput)}
	generator := NewGenerator(repository, objectStore, 24*time.Hour)
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
	err := NewGenerator(repository, objectStore, time.Hour).Handle(context.Background(), jobs.Job{
		Kind: exportjob.KindGenerate, Payload: payload, Attempts: 5, MaxAttempts: 5,
	})
	if err == nil || repository.failedCode != "generation_failed" {
		t.Fatalf("error = %v, failure code = %q", err, repository.failedCode)
	}
}

func TestCleanupAndDeletionRemovePrivateResult(t *testing.T) {
	t.Parallel()
	treeID, exportID := uuid.New(), uuid.New()
	key := ResultObjectKey(treeID, exportID, "checksum")
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
