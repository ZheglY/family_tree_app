package processing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/jobs"
	"github.com/ZheglY/family_tree_app/internal/core/storage"
	"github.com/ZheglY/family_tree_app/internal/features/exports/domain"
	"github.com/ZheglY/family_tree_app/internal/features/exports/exportjob"
	"github.com/google/uuid"
)

type CleanupRepository interface {
	ReserveCleanupCandidates(context.Context, time.Time, int, time.Time) ([]domain.CleanupCandidate, error)
	GetDeletionCandidate(context.Context, uuid.UUID, uuid.UUID) (domain.CleanupCandidate, error)
	ClearExpiredResult(context.Context, domain.CleanupCandidate, time.Time) error
}

type Cleanup struct {
	repository  CleanupRepository
	objectStore storage.ProcessorObjectStore
	now         func() time.Time
}

func NewCleanup(repository CleanupRepository, objectStore storage.ProcessorObjectStore) *Cleanup {
	return &Cleanup{
		repository: repository, objectStore: objectStore,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (cleanup *Cleanup) Handle(ctx context.Context, job jobs.Job) error {
	var payload exportjob.CleanupPayload
	if job.Kind != exportjob.KindCleanup || json.Unmarshal(job.Payload, &payload) != nil ||
		payload.ExpiredBefore.IsZero() || payload.BatchSize < 1 || payload.BatchSize > 1000 {
		return fmt.Errorf("invalid export cleanup payload")
	}
	candidates, err := cleanup.repository.ReserveCleanupCandidates(
		ctx, payload.ExpiredBefore, payload.BatchSize, cleanup.now(),
	)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if err := cleanup.deleteCandidate(ctx, candidate); err != nil {
			return err
		}
	}
	return nil
}

type Deleter struct {
	cleanup *Cleanup
}

func NewDeleter(repository CleanupRepository, objectStore storage.ProcessorObjectStore) *Deleter {
	return &Deleter{cleanup: NewCleanup(repository, objectStore)}
}

func (deleter *Deleter) Handle(ctx context.Context, job jobs.Job) error {
	var payload exportjob.DeletePayload
	if job.Kind != exportjob.KindDelete || json.Unmarshal(job.Payload, &payload) != nil ||
		payload.TreeID == uuid.Nil || payload.ExportID == uuid.Nil {
		return fmt.Errorf("invalid export deletion payload")
	}
	candidate, err := deleter.cleanup.repository.GetDeletionCandidate(ctx, payload.TreeID, payload.ExportID)
	if errors.Is(err, domain.ErrExportNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return deleter.cleanup.deleteCandidate(ctx, candidate)
}

func (cleanup *Cleanup) deleteCandidate(ctx context.Context, candidate domain.CleanupCandidate) error {
	if candidate.ResultObjectKey != "" {
		if err := cleanup.objectStore.DeleteObject(ctx, candidate.ResultObjectKey); err != nil {
			return err
		}
	}
	return cleanup.repository.ClearExpiredResult(ctx, candidate, cleanup.now())
}
