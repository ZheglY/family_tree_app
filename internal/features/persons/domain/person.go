package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	SexMale         = "male"
	SexFemale       = "female"
	SexUnknown      = "unknown"
	SexNotSpecified = "not_specified"

	LifeStatusAlive    = "alive"
	LifeStatusDeceased = "deceased"
	LifeStatusUnknown  = "unknown"

	PrivacyTreeMembers = "tree_members"
	NameTypePrimary    = "primary"

	maxBiographyRunes = 50000
	maxNotesRunes     = 20000
	maxNameRunes      = 150
	maxAffixRunes     = 50
)

var languagePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)

type Person struct {
	ID             uuid.UUID
	TreeID         uuid.UUID
	Sex            string
	LifeStatus     string
	Biography      string
	Notes          string
	PrimaryMediaID *uuid.UUID
	PrivacyLevel   string
	CreatedBy      uuid.UUID
	UpdatedBy      uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      *time.Time
	Version        int
}

type PersonName struct {
	ID           uuid.UUID
	PersonID     uuid.UUID
	TreeID       uuid.UUID
	Type         string
	GivenName    string
	Patronymic   string
	FamilyName   string
	Prefix       string
	Suffix       string
	FullText     string
	IsPreferred  bool
	LanguageCode string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Card struct {
	Person        Person
	PreferredName PersonName
}

type NameValues struct {
	GivenName    string
	Patronymic   string
	FamilyName   string
	Prefix       string
	Suffix       string
	LanguageCode string
}

type CreateValues struct {
	Sex        string
	LifeStatus string
	Biography  string
	Notes      string
	Name       NameValues
}

type UpdateValues struct {
	Sex           *string
	LifeStatus    *string
	Biography     *string
	Notes         *string
	PreferredName *NameValues
}

func New(
	personID uuid.UUID,
	nameID uuid.UUID,
	treeID uuid.UUID,
	actorUserID uuid.UUID,
	values CreateValues,
	now time.Time,
) (Card, error) {
	if personID == uuid.Nil || nameID == uuid.Nil || treeID == uuid.Nil ||
		actorUserID == uuid.Nil || now.IsZero() {
		return Card{}, ErrInvalidPerson
	}
	sex := values.Sex
	if sex == "" {
		sex = SexNotSpecified
	}
	lifeStatus := values.LifeStatus
	if lifeStatus == "" {
		lifeStatus = LifeStatusUnknown
	}
	biography, notes, err := normalizePersonValues(sex, lifeStatus, values.Biography, values.Notes)
	if err != nil {
		return Card{}, err
	}
	name, err := newPreferredName(nameID, personID, treeID, values.Name, now)
	if err != nil {
		return Card{}, err
	}
	return Card{
		Person: Person{
			ID:           personID,
			TreeID:       treeID,
			Sex:          sex,
			LifeStatus:   lifeStatus,
			Biography:    biography,
			Notes:        notes,
			PrivacyLevel: PrivacyTreeMembers,
			CreatedBy:    actorUserID,
			UpdatedBy:    actorUserID,
			CreatedAt:    now,
			UpdatedAt:    now,
			Version:      1,
		},
		PreferredName: name,
	}, nil
}

func ApplyUpdate(
	card Card,
	expectedVersion int,
	actorUserID uuid.UUID,
	values UpdateValues,
	now time.Time,
) (Card, error) {
	if expectedVersion <= 0 || card.Person.Version != expectedVersion {
		return Card{}, ErrPersonVersionConflict
	}
	if actorUserID == uuid.Nil || now.IsZero() {
		return Card{}, ErrInvalidPerson
	}
	if card.Person.DeletedAt != nil {
		return Card{}, ErrPersonNotFound
	}
	if values.Sex == nil && values.LifeStatus == nil && values.Biography == nil &&
		values.Notes == nil && values.PreferredName == nil {
		return Card{}, fmt.Errorf("%w: no fields to update", ErrInvalidPerson)
	}
	sex := card.Person.Sex
	lifeStatus := card.Person.LifeStatus
	biography := card.Person.Biography
	notes := card.Person.Notes
	if values.Sex != nil {
		sex = *values.Sex
	}
	if values.LifeStatus != nil {
		lifeStatus = *values.LifeStatus
	}
	if values.Biography != nil {
		biography = *values.Biography
	}
	if values.Notes != nil {
		notes = *values.Notes
	}
	biography, notes, err := normalizePersonValues(sex, lifeStatus, biography, notes)
	if err != nil {
		return Card{}, err
	}
	if values.PreferredName != nil {
		name, err := newPreferredName(
			card.PreferredName.ID,
			card.Person.ID,
			card.Person.TreeID,
			*values.PreferredName,
			now,
		)
		if err != nil {
			return Card{}, err
		}
		name.CreatedAt = card.PreferredName.CreatedAt
		card.PreferredName = name
	}
	card.Person.Sex = sex
	card.Person.LifeStatus = lifeStatus
	card.Person.Biography = biography
	card.Person.Notes = notes
	card.Person.UpdatedBy = actorUserID
	card.Person.UpdatedAt = now
	card.Person.Version++
	return card, nil
}

func IsLifeStatus(value string) bool {
	switch value {
	case LifeStatusAlive, LifeStatusDeceased, LifeStatusUnknown:
		return true
	default:
		return false
	}
}

func newPreferredName(
	id uuid.UUID,
	personID uuid.UUID,
	treeID uuid.UUID,
	values NameValues,
	now time.Time,
) (PersonName, error) {
	values.GivenName = strings.TrimSpace(values.GivenName)
	values.Patronymic = strings.TrimSpace(values.Patronymic)
	values.FamilyName = strings.TrimSpace(values.FamilyName)
	values.Prefix = strings.TrimSpace(values.Prefix)
	values.Suffix = strings.TrimSpace(values.Suffix)
	values.LanguageCode = strings.TrimSpace(values.LanguageCode)
	if values.LanguageCode == "" {
		values.LanguageCode = "ru"
	}
	if utf8.RuneCountInString(values.GivenName) > maxNameRunes ||
		utf8.RuneCountInString(values.Patronymic) > maxNameRunes ||
		utf8.RuneCountInString(values.FamilyName) > maxNameRunes ||
		utf8.RuneCountInString(values.Prefix) > maxAffixRunes ||
		utf8.RuneCountInString(values.Suffix) > maxAffixRunes ||
		!languagePattern.MatchString(values.LanguageCode) {
		return PersonName{}, ErrInvalidPerson
	}
	fullText := strings.Join(nonEmpty(
		values.Prefix,
		values.GivenName,
		values.Patronymic,
		values.FamilyName,
		values.Suffix,
	), " ")
	if fullText == "" {
		return PersonName{}, fmt.Errorf("%w: preferred name is empty", ErrInvalidPerson)
	}
	return PersonName{
		ID:           id,
		PersonID:     personID,
		TreeID:       treeID,
		Type:         NameTypePrimary,
		GivenName:    values.GivenName,
		Patronymic:   values.Patronymic,
		FamilyName:   values.FamilyName,
		Prefix:       values.Prefix,
		Suffix:       values.Suffix,
		FullText:     fullText,
		IsPreferred:  true,
		LanguageCode: values.LanguageCode,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

func normalizePersonValues(
	sex string,
	lifeStatus string,
	biography string,
	notes string,
) (string, string, error) {
	if !isSex(sex) || !IsLifeStatus(lifeStatus) ||
		utf8.RuneCountInString(biography) > maxBiographyRunes ||
		utf8.RuneCountInString(notes) > maxNotesRunes {
		return "", "", ErrInvalidPerson
	}
	return strings.TrimSpace(biography), strings.TrimSpace(notes), nil
}

func isSex(value string) bool {
	switch value {
	case SexMale, SexFemale, SexUnknown, SexNotSpecified:
		return true
	default:
		return false
	}
}

func nonEmpty(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}
