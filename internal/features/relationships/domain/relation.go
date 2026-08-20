package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	RelationBiological = "biological"
	RelationAdoptive   = "adoptive"
	RelationFoster     = "foster"
	RelationGuardian   = "guardian"
	RelationStep       = "step"
	RelationUnknown    = "unknown"

	ConfidenceUnverified = "unverified"
	ConfidenceProbable   = "probable"
	ConfidenceConfirmed  = "confirmed"
	ConfidenceDisputed   = "disputed"

	maxNoteRunes = 5000
)

type ParentChildRelation struct {
	ID             uuid.UUID
	TreeID         uuid.UUID
	ParentPersonID uuid.UUID
	ChildPersonID  uuid.UUID
	RelationType   string
	Confidence     string
	Note           string
	CreatedBy      uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      *time.Time
	Version        int
}

type CreateValues struct {
	ParentPersonID uuid.UUID
	ChildPersonID  uuid.UUID
	RelationType   string
	Confidence     string
	Note           string
}

type UpdateValues struct {
	RelationType *string
	Confidence   *string
	Note         *string
}

type PersonSummary struct {
	ID             uuid.UUID
	Sex            string
	LifeStatus     string
	PrimaryMediaID *uuid.UUID
	PreferredName  PreferredNameSummary
}

type PreferredNameSummary struct {
	ID           uuid.UUID
	GivenName    string
	Patronymic   string
	FamilyName   string
	Prefix       string
	Suffix       string
	FullText     string
	LanguageCode string
}

type Graph struct {
	CenterPersonID uuid.UUID
	Persons        []PersonSummary
	Relations      []ParentChildRelation
}

func New(
	id uuid.UUID,
	treeID uuid.UUID,
	actorUserID uuid.UUID,
	values CreateValues,
	now time.Time,
) (ParentChildRelation, error) {
	if id == uuid.Nil || treeID == uuid.Nil || actorUserID == uuid.Nil || now.IsZero() ||
		values.ParentPersonID == uuid.Nil || values.ChildPersonID == uuid.Nil {
		return ParentChildRelation{}, ErrInvalidRelation
	}
	if values.ParentPersonID == values.ChildPersonID {
		return ParentChildRelation{}, fmt.Errorf("%w: a person cannot be their own parent", ErrInvalidRelation)
	}
	relationType := values.RelationType
	if relationType == "" {
		relationType = RelationUnknown
	}
	confidence := values.Confidence
	if confidence == "" {
		confidence = ConfidenceUnverified
	}
	note, err := validateValues(relationType, confidence, values.Note)
	if err != nil {
		return ParentChildRelation{}, err
	}
	return ParentChildRelation{
		ID:             id,
		TreeID:         treeID,
		ParentPersonID: values.ParentPersonID,
		ChildPersonID:  values.ChildPersonID,
		RelationType:   relationType,
		Confidence:     confidence,
		Note:           note,
		CreatedBy:      actorUserID,
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}, nil
}

func ApplyUpdate(
	relation ParentChildRelation,
	expectedVersion int,
	values UpdateValues,
	now time.Time,
) (ParentChildRelation, error) {
	if expectedVersion <= 0 || relation.Version != expectedVersion {
		return ParentChildRelation{}, ErrRelationVersionConflict
	}
	if relation.DeletedAt != nil {
		return ParentChildRelation{}, ErrRelationNotFound
	}
	if now.IsZero() {
		return ParentChildRelation{}, ErrInvalidRelation
	}
	if values.RelationType == nil && values.Confidence == nil && values.Note == nil {
		return ParentChildRelation{}, fmt.Errorf("%w: no fields to update", ErrInvalidRelation)
	}
	relationType := relation.RelationType
	confidence := relation.Confidence
	note := relation.Note
	if values.RelationType != nil {
		relationType = *values.RelationType
	}
	if values.Confidence != nil {
		confidence = *values.Confidence
	}
	if values.Note != nil {
		note = *values.Note
	}
	note, err := validateValues(relationType, confidence, note)
	if err != nil {
		return ParentChildRelation{}, err
	}
	relation.RelationType = relationType
	relation.Confidence = confidence
	relation.Note = note
	relation.UpdatedAt = now
	relation.Version++
	return relation, nil
}

func validateValues(relationType string, confidence string, note string) (string, error) {
	note = strings.TrimSpace(note)
	if !IsRelationType(relationType) || !IsConfidence(confidence) ||
		utf8.RuneCountInString(note) > maxNoteRunes {
		return "", ErrInvalidRelation
	}
	return note, nil
}

func IsRelationType(value string) bool {
	switch value {
	case RelationBiological, RelationAdoptive, RelationFoster,
		RelationGuardian, RelationStep, RelationUnknown:
		return true
	default:
		return false
	}
}

func IsConfidence(value string) bool {
	switch value {
	case ConfidenceUnverified, ConfidenceProbable,
		ConfidenceConfirmed, ConfidenceDisputed:
		return true
	default:
		return false
	}
}
