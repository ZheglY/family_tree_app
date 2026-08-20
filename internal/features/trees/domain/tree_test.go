package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewFamilyTreeCreatesActiveOwner(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	treeID := uuid.New()
	ownerID := uuid.New()

	access, err := NewFamilyTree(treeID, ownerID, CreateValues{
		Name:        "  Род Романовых  ",
		Description: " История семьи ",
		Locale:      "ru-RU",
		Timezone:    "Europe/Moscow",
	}, now)
	if err != nil {
		t.Fatalf("NewFamilyTree() error = %v", err)
	}
	if access.Tree.Name != "Род Романовых" || access.Tree.Version != 1 ||
		access.Tree.Privacy != PrivacyPrivate {
		t.Fatalf("tree = %#v", access.Tree)
	}
	if access.Membership.TreeID != treeID || access.Membership.UserID != ownerID ||
		access.Membership.Role != RoleOwner ||
		access.Membership.Status != MemberStatusActive ||
		access.Membership.AcceptedAt == nil {
		t.Fatalf("membership = %#v", access.Membership)
	}
}

func TestNewFamilyTreeRejectsInvalidValues(t *testing.T) {
	t.Parallel()
	tests := []CreateValues{
		{Name: "", Locale: "ru-RU", Timezone: "UTC"},
		{Name: "Tree", Locale: "invalid locale", Timezone: "UTC"},
		{Name: "Tree", Locale: "ru-RU", Timezone: "Mars/Olympus"},
	}
	for _, values := range tests {
		_, err := NewFamilyTree(uuid.New(), uuid.New(), values, time.Now().UTC())
		if !errors.Is(err, ErrInvalidTree) {
			t.Fatalf("NewFamilyTree(%#v) error = %v, want ErrInvalidTree", values, err)
		}
	}
}

func TestApplyUpdateUsesOptimisticVersion(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	access, err := NewFamilyTree(uuid.New(), uuid.New(), CreateValues{
		Name: "Tree", Locale: "ru-RU", Timezone: "UTC",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	name := "Updated tree"
	updated, err := ApplyUpdate(access.Tree, 1, UpdateValues{Name: &name}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ApplyUpdate() error = %v", err)
	}
	if updated.Name != name || updated.Version != 2 {
		t.Fatalf("updated tree = %#v", updated)
	}
	if _, err := ApplyUpdate(updated, 1, UpdateValues{Name: &name}, now); !errors.Is(err, ErrTreeVersionConflict) {
		t.Fatalf("stale ApplyUpdate() error = %v", err)
	}
	if _, err := ApplyUpdate(updated, 2, UpdateValues{}, now); !errors.Is(err, ErrInvalidTree) {
		t.Fatalf("empty ApplyUpdate() error = %v", err)
	}
}
