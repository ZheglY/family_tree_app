package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewRelationUsesSafeDefaults(t *testing.T) {
	t.Parallel()
	relation, err := New(
		uuid.New(),
		uuid.New(),
		uuid.New(),
		CreateValues{ParentPersonID: uuid.New(), ChildPersonID: uuid.New()},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if relation.RelationType != RelationUnknown ||
		relation.Confidence != ConfidenceUnverified || relation.Version != 1 {
		t.Fatalf("relation = %#v", relation)
	}
}

func TestNewRelationRejectsSelfParent(t *testing.T) {
	t.Parallel()
	personID := uuid.New()
	_, err := New(
		uuid.New(),
		uuid.New(),
		uuid.New(),
		CreateValues{ParentPersonID: personID, ChildPersonID: personID},
		time.Now().UTC(),
	)
	if !errors.Is(err, ErrInvalidRelation) {
		t.Fatalf("New() error = %v, want ErrInvalidRelation", err)
	}
}

func TestApplyUpdateUsesOptimisticVersion(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	relation, err := New(
		uuid.New(),
		uuid.New(),
		uuid.New(),
		CreateValues{
			ParentPersonID: uuid.New(),
			ChildPersonID:  uuid.New(),
			RelationType:   RelationBiological,
			Confidence:     ConfidenceProbable,
		},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	confidence := ConfidenceConfirmed
	updated, err := ApplyUpdate(
		relation,
		1,
		UpdateValues{Confidence: &confidence},
		now.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("ApplyUpdate() error = %v", err)
	}
	if updated.Confidence != ConfidenceConfirmed || updated.Version != 2 {
		t.Fatalf("updated relation = %#v", updated)
	}
	if _, err := ApplyUpdate(updated, 1, UpdateValues{Confidence: &confidence}, now); !errors.Is(err, ErrRelationVersionConflict) {
		t.Fatalf("stale ApplyUpdate() error = %v", err)
	}
}
