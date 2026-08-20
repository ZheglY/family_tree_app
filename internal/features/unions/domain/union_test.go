package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewUnionUsesDefaultsAndBuildsMembers(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	aggregate, err := New(uuid.New(), uuid.New(), uuid.New(), CreateValues{
		Members: []MemberValues{
			{PersonID: uuid.New(), Role: " spouse "},
			{PersonID: uuid.New()},
		},
	}, now)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if aggregate.Union.Type != TypeUnknown || aggregate.Union.Version != 1 {
		t.Fatalf("union = %#v", aggregate.Union)
	}
	if len(aggregate.Members) != 2 || aggregate.Members[0].Role != "spouse" {
		t.Fatalf("members = %#v", aggregate.Members)
	}
}

func TestNewUnionRejectsInvalidMemberSet(t *testing.T) {
	t.Parallel()
	personID := uuid.New()
	base := func(members []MemberValues) error {
		_, err := New(uuid.New(), uuid.New(), uuid.New(), CreateValues{Members: members}, time.Now().UTC())
		return err
	}
	if err := base([]MemberValues{{PersonID: personID}}); !errors.Is(err, ErrUnionMemberLimit) {
		t.Fatalf("single member error = %v", err)
	}
	if err := base([]MemberValues{{PersonID: personID}, {PersonID: personID}}); !errors.Is(err, ErrDuplicateUnionMember) {
		t.Fatalf("duplicate member error = %v", err)
	}
}

func TestApplyUpdateUsesOptimisticVersion(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	aggregate, err := New(uuid.New(), uuid.New(), uuid.New(), CreateValues{
		Type: TypeMarriage,
		Members: []MemberValues{
			{PersonID: uuid.New()},
			{PersonID: uuid.New()},
		},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	unionType := TypeCivilUnion
	note := " Архивная запись "
	updated, err := ApplyUpdate(
		aggregate.Union,
		1,
		uuid.New(),
		UpdateValues{Type: &unionType, Note: &note},
		now.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("ApplyUpdate() error = %v", err)
	}
	if updated.Type != TypeCivilUnion || updated.Note != "Архивная запись" || updated.Version != 2 {
		t.Fatalf("updated union = %#v", updated)
	}
	if _, err := ApplyUpdate(updated, 1, uuid.New(), UpdateValues{Note: &note}, now); !errors.Is(err, ErrUnionVersionConflict) {
		t.Fatalf("stale ApplyUpdate() error = %v", err)
	}
}
