package processing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/jobs"
	"github.com/ZheglY/family_tree_app/internal/core/storage"
	"github.com/ZheglY/family_tree_app/internal/features/exports/domain"
	"github.com/ZheglY/family_tree_app/internal/features/exports/exportjob"
	"github.com/ZheglY/family_tree_app/internal/features/exports/manifest"
	"github.com/google/uuid"
)

const manifestMIMEType = "application/json"

const archiveMIMEType = "application/zip"

type GeneratorRepository interface {
	AcquireForGeneration(context.Context, uuid.UUID, uuid.UUID, time.Time) (domain.Export, error)
	LoadManifest(context.Context, domain.Export) (manifest.Snapshot, error)
	MarkCompleted(context.Context, domain.Export, string, string, int64, string, time.Time, time.Time) error
	MarkFailed(context.Context, domain.Export, string, time.Time) error
}

type Generator struct {
	repository      GeneratorRepository
	objectStore     storage.ProcessorObjectStore
	resultTTL       time.Duration
	maxArchiveBytes int64
	now             func() time.Time
}

func NewGenerator(
	repository GeneratorRepository,
	objectStore storage.ProcessorObjectStore,
	resultTTL time.Duration,
	maxArchiveBytes int64,
) *Generator {
	return &Generator{
		repository: repository, objectStore: objectStore, resultTTL: resultTTL,
		maxArchiveBytes: maxArchiveBytes,
		now:             func() time.Time { return time.Now().UTC() },
	}
}

func (generator *Generator) Handle(ctx context.Context, job jobs.Job) error {
	var payload exportjob.GeneratePayload
	if job.Kind != exportjob.KindGenerate || json.Unmarshal(job.Payload, &payload) != nil ||
		payload.TreeID == uuid.Nil || payload.ExportID == uuid.Nil {
		return fmt.Errorf("invalid export generation payload")
	}
	export, err := generator.repository.AcquireForGeneration(ctx, payload.TreeID, payload.ExportID, generator.now())
	if errors.Is(err, domain.ErrExportNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if export.Status == domain.StatusCompleted || export.Status == domain.StatusFailed ||
		export.Status == domain.StatusExpired {
		return nil
	}
	if export.Status != domain.StatusRunning {
		return domain.ErrExportStateConflict
	}
	snapshot, err := generator.repository.LoadManifest(ctx, export)
	if err != nil {
		if errors.Is(err, domain.ErrExportTreeUnavailable) {
			if markErr := generator.repository.MarkFailed(ctx, export, "tree_unavailable", generator.now()); markErr != nil {
				return errors.Join(err, markErr)
			}
			return nil
		}
		return generator.retryOrFail(ctx, job, export, err)
	}
	var body []byte
	var mimeType string
	switch export.Format {
	case domain.FormatJSONBackup:
		body, err = encodeManifest(snapshot.Manifest)
		mimeType = manifestMIMEType
	case domain.FormatZIPBackup:
		body, err = buildZIP(ctx, export, snapshot, generator.objectStore, generator.maxArchiveBytes)
		mimeType = archiveMIMEType
	default:
		err = domain.ErrInvalidExport
	}
	if err != nil {
		if errors.Is(err, domain.ErrExportArchiveTooLarge) {
			if markErr := generator.repository.MarkFailed(ctx, export, "archive_too_large", generator.now()); markErr != nil {
				return errors.Join(err, markErr)
			}
			return nil
		}
		return generator.retryOrFail(ctx, job, export, err)
	}
	checksum := sha256.Sum256(body)
	checksumHex := hex.EncodeToString(checksum[:])
	objectKey := ResultObjectKey(export.TreeID, export.ID, export.Format, checksumHex)
	if _, err := generator.objectStore.PutObject(ctx, storage.PutInput{
		ObjectKey: objectKey, ContentType: mimeType, ChecksumSHA256: checksumHex, Body: body,
	}); err != nil {
		return generator.retryOrFail(ctx, job, export, err)
	}
	now := generator.now()
	err = generator.repository.MarkCompleted(
		ctx, export, objectKey, mimeType, int64(len(body)), checksumHex,
		now.Add(generator.resultTTL), now,
	)
	if errors.Is(err, domain.ErrExportStateConflict) {
		if deleteErr := generator.objectStore.DeleteObject(ctx, objectKey); deleteErr != nil {
			return errors.Join(err, deleteErr)
		}
		return nil
	}
	if err != nil {
		return generator.retryOrFail(ctx, job, export, err)
	}
	return nil
}

func (generator *Generator) retryOrFail(
	ctx context.Context,
	job jobs.Job,
	export domain.Export,
	cause error,
) error {
	if job.Attempts < job.MaxAttempts {
		return cause
	}
	if err := generator.repository.MarkFailed(ctx, export, "generation_failed", generator.now()); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func ResultObjectKey(treeID uuid.UUID, exportID uuid.UUID, format string, checksum string) string {
	filename := fmt.Sprintf("manifest-%s.json", checksum)
	if format == domain.FormatZIPBackup {
		filename = fmt.Sprintf("backup-%s.zip", checksum)
	}
	return fmt.Sprintf("trees/%s/exports/%s/%s", treeID, exportID, filename)
}

func encodeManifest(value manifest.Manifest) ([]byte, error) {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode export manifest: %w", err)
	}
	return append(body, '\n'), nil
}
