package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewCreatesPreferredNameAndDefaults(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 20, 15, 0, 0, 0, time.UTC)
	card, err := New(
		uuid.New(),
		uuid.New(),
		uuid.New(),
		uuid.New(),
		CreateValues{Name: NameValues{
			GivenName:  " Александр ",
			Patronymic: " Сергеевич ",
			FamilyName: " Пушкин ",
		}},
		now,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if card.Person.Sex != SexNotSpecified || card.Person.LifeStatus != LifeStatusUnknown ||
		card.Person.PrivacyLevel != PrivacyTreeMembers || card.Person.Version != 1 {
		t.Fatalf("person = %#v", card.Person)
	}
	if card.PreferredName.FullText != "Александр Сергеевич Пушкин" ||
		!card.PreferredName.IsPreferred || card.PreferredName.LanguageCode != "ru" {
		t.Fatalf("preferred name = %#v", card.PreferredName)
	}
}

func TestNewRejectsEmptyPreferredName(t *testing.T) {
	t.Parallel()
	_, err := New(uuid.New(), uuid.New(), uuid.New(), uuid.New(), CreateValues{}, time.Now().UTC())
	if !errors.Is(err, ErrInvalidPerson) {
		t.Fatalf("New() error = %v, want ErrInvalidPerson", err)
	}
}

func TestApplyUpdateChangesAggregateVersion(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 20, 15, 0, 0, 0, time.UTC)
	actorID := uuid.New()
	card, err := New(
		uuid.New(), uuid.New(), uuid.New(), actorID,
		CreateValues{Name: NameValues{GivenName: "Анна", FamilyName: "Волконская"}},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	biography := "Новая биография"
	name := NameValues{GivenName: "Анна", FamilyName: "Болконская", LanguageCode: "ru"}
	updated, err := ApplyUpdate(card, 1, actorID, UpdateValues{
		Biography:     &biography,
		PreferredName: &name,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ApplyUpdate() error = %v", err)
	}
	if updated.Person.Version != 2 || updated.Person.Biography != biography ||
		updated.PreferredName.FullText != "Анна Болконская" {
		t.Fatalf("updated = %#v", updated)
	}
	if _, err := ApplyUpdate(updated, 1, actorID, UpdateValues{Biography: &biography}, now); !errors.Is(err, ErrPersonVersionConflict) {
		t.Fatalf("stale ApplyUpdate() error = %v", err)
	}
}
