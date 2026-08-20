package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	TypeMarriage    = "marriage"
	TypeCivilUnion  = "civil_union"
	TypePartnership = "partnership"
	TypeEngagement  = "engagement"
	TypeUnknown     = "unknown"

	MinMembers = 2
	MaxMembers = 10

	maxEndReasonRunes = 500
	maxNoteRunes      = 5000
	maxRoleRunes      = 50
)

type FamilyUnion struct {
	ID        uuid.UUID
	TreeID    uuid.UUID
	Type      string
	EndReason string
	Note      string
	CreatedBy uuid.UUID
	UpdatedBy uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
	Version   int
}

type UnionMember struct {
	UnionID   uuid.UUID
	PersonID  uuid.UUID
	TreeID    uuid.UUID
	Role      string
	CreatedAt time.Time
}

type Aggregate struct {
	Union   FamilyUnion
	Members []UnionMember
}

type MemberValues struct {
	PersonID uuid.UUID
	Role     string
}

type CreateValues struct {
	Type      string
	EndReason string
	Note      string
	Members   []MemberValues
}

type UpdateValues struct {
	Type      *string
	EndReason *string
	Note      *string
}

func New(
	id uuid.UUID,
	treeID uuid.UUID,
	actorUserID uuid.UUID,
	values CreateValues,
	now time.Time,
) (Aggregate, error) {
	if id == uuid.Nil || treeID == uuid.Nil || actorUserID == uuid.Nil || now.IsZero() {
		return Aggregate{}, ErrInvalidUnion
	}
	unionType := values.Type
	if unionType == "" {
		unionType = TypeUnknown
	}
	endReason, note, err := validateUnionValues(unionType, values.EndReason, values.Note)
	if err != nil {
		return Aggregate{}, err
	}
	if len(values.Members) < MinMembers || len(values.Members) > MaxMembers {
		return Aggregate{}, ErrUnionMemberLimit
	}
	members := make([]UnionMember, 0, len(values.Members))
	seen := make(map[uuid.UUID]struct{}, len(values.Members))
	for _, value := range values.Members {
		if value.PersonID == uuid.Nil {
			return Aggregate{}, ErrInvalidUnion
		}
		if _, exists := seen[value.PersonID]; exists {
			return Aggregate{}, ErrDuplicateUnionMember
		}
		seen[value.PersonID] = struct{}{}
		member, err := NewMember(id, treeID, value, now)
		if err != nil {
			return Aggregate{}, err
		}
		members = append(members, member)
	}
	return Aggregate{
		Union: FamilyUnion{
			ID:        id,
			TreeID:    treeID,
			Type:      unionType,
			EndReason: endReason,
			Note:      note,
			CreatedBy: actorUserID,
			UpdatedBy: actorUserID,
			CreatedAt: now,
			UpdatedAt: now,
			Version:   1,
		},
		Members: members,
	}, nil
}

func NewMember(
	unionID uuid.UUID,
	treeID uuid.UUID,
	values MemberValues,
	now time.Time,
) (UnionMember, error) {
	role := strings.TrimSpace(values.Role)
	if unionID == uuid.Nil || treeID == uuid.Nil || values.PersonID == uuid.Nil ||
		now.IsZero() || utf8.RuneCountInString(role) > maxRoleRunes {
		return UnionMember{}, ErrInvalidUnion
	}
	return UnionMember{
		UnionID:   unionID,
		PersonID:  values.PersonID,
		TreeID:    treeID,
		Role:      role,
		CreatedAt: now,
	}, nil
}

func ApplyUpdate(
	union FamilyUnion,
	expectedVersion int,
	actorUserID uuid.UUID,
	values UpdateValues,
	now time.Time,
) (FamilyUnion, error) {
	if expectedVersion <= 0 || union.Version != expectedVersion {
		return FamilyUnion{}, ErrUnionVersionConflict
	}
	if union.DeletedAt != nil {
		return FamilyUnion{}, ErrUnionNotFound
	}
	if actorUserID == uuid.Nil || now.IsZero() {
		return FamilyUnion{}, ErrInvalidUnion
	}
	if values.Type == nil && values.EndReason == nil && values.Note == nil {
		return FamilyUnion{}, fmt.Errorf("%w: no fields to update", ErrInvalidUnion)
	}
	unionType := union.Type
	endReason := union.EndReason
	note := union.Note
	if values.Type != nil {
		unionType = *values.Type
	}
	if values.EndReason != nil {
		endReason = *values.EndReason
	}
	if values.Note != nil {
		note = *values.Note
	}
	endReason, note, err := validateUnionValues(unionType, endReason, note)
	if err != nil {
		return FamilyUnion{}, err
	}
	union.Type = unionType
	union.EndReason = endReason
	union.Note = note
	union.UpdatedBy = actorUserID
	union.UpdatedAt = now
	union.Version++
	return union, nil
}

func validateUnionValues(unionType string, endReason string, note string) (string, string, error) {
	endReason = strings.TrimSpace(endReason)
	note = strings.TrimSpace(note)
	if !IsType(unionType) || utf8.RuneCountInString(endReason) > maxEndReasonRunes ||
		utf8.RuneCountInString(note) > maxNoteRunes {
		return "", "", ErrInvalidUnion
	}
	return endReason, note, nil
}

func IsType(value string) bool {
	switch value {
	case TypeMarriage, TypeCivilUnion, TypePartnership, TypeEngagement, TypeUnknown:
		return true
	default:
		return false
	}
}
