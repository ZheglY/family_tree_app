package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/jobs"
	"github.com/ZheglY/family_tree_app/internal/core/storage"
	"github.com/ZheglY/family_tree_app/internal/features/media/domain"
	"github.com/ZheglY/family_tree_app/internal/features/media/mediajob"
)

type CleanupRepository interface {
	ListCleanupCandidates(context.Context, time.Time, time.Time, int) ([]domain.CleanupCandidate, error)
	ReserveCleanupCandidate(context.Context, domain.CleanupCandidate, time.Time, time.Time) (bool, error)
	DeleteCleanupCandidate(context.Context, domain.CleanupCandidate, time.Time, time.Time) error
}

type Cleanup struct {
	repository  CleanupRepository
	objectStore storage.ProcessorObjectStore
}

func NewCleanup(repository CleanupRepository, objectStore storage.ProcessorObjectStore) *Cleanup {
	return &Cleanup{repository: repository, objectStore: objectStore}
}

func (cleanup *Cleanup) Handle(ctx context.Context, job jobs.Job) error {
	var payload mediajob.CleanupPayload
	if job.Kind != mediajob.KindCleanup || json.Unmarshal(job.Payload, &payload) != nil ||
		payload.PendingBefore.IsZero() || payload.DeletedBefore.IsZero() ||
		payload.BatchSize < 1 || payload.BatchSize > 1000 {
		return fmt.Errorf("invalid media cleanup payload")
	}
	candidates, err := cleanup.repository.ListCleanupCandidates(
		ctx,
		payload.PendingBefore,
		payload.DeletedBefore,
		payload.BatchSize,
	)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		reserved, err := cleanup.repository.ReserveCleanupCandidate(
			ctx,
			candidate,
			payload.PendingBefore,
			time.Now().UTC(),
		)
		if err != nil {
			return err
		}
		if !reserved {
			continue
		}
		keys := append([]string{candidate.ObjectKey}, candidate.VariantKeys...)
		base := strings.TrimSuffix(candidate.ObjectKey, "/original")
		keys = append(keys,
			base+"/variants/thumbnail.jpg",
			base+"/variants/preview.jpg",
		)
		seen := make(map[string]struct{}, len(keys))
		for _, key := range keys {
			if key == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			if err := cleanup.objectStore.DeleteObject(ctx, key); err != nil {
				return err
			}
		}
		if err := cleanup.repository.DeleteCleanupCandidate(
			ctx,
			candidate,
			payload.PendingBefore,
			payload.DeletedBefore,
		); err != nil {
			return err
		}
	}
	return nil
}
