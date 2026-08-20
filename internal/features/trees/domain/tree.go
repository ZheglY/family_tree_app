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
	PrivacyPrivate      = "private"
	RoleOwner           = "owner"
	RoleEditor          = "editor"
	RoleViewer          = "viewer"
	MemberStatusActive  = "active"
	maxNameRunes        = 150
	maxDescriptionRunes = 5000
	maxLocaleBytes      = 35
	maxTimezoneBytes    = 100
)

var localePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)

type FamilyTree struct {
	ID           uuid.UUID
	Name         string
	Description  string
	OwnerUserID  uuid.UUID
	RootPersonID *uuid.UUID
	CoverMediaID *uuid.UUID
	Privacy      string
	Locale       string
	Timezone     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time
	Version      int
}

type TreeMember struct {
	TreeID     uuid.UUID
	UserID     uuid.UUID
	Role       string
	Status     string
	InvitedBy  *uuid.UUID
	CreatedAt  time.Time
	AcceptedAt *time.Time
}

type TreeAccess struct {
	Tree       FamilyTree
	Membership TreeMember
}

type CreateValues struct {
	Name        string
	Description string
	Locale      string
	Timezone    string
}

type UpdateValues struct {
	Name        *string
	Description *string
	Locale      *string
	Timezone    *string
}

func NewFamilyTree(
	id uuid.UUID,
	ownerUserID uuid.UUID,
	values CreateValues,
	now time.Time,
) (TreeAccess, error) {
	if id == uuid.Nil || ownerUserID == uuid.Nil || now.IsZero() {
		return TreeAccess{}, ErrInvalidTree
	}
	name, description, locale, timezone, err := normalizeValues(
		values.Name,
		values.Description,
		values.Locale,
		values.Timezone,
	)
	if err != nil {
		return TreeAccess{}, err
	}
	acceptedAt := now
	tree := FamilyTree{
		ID:          id,
		Name:        name,
		Description: description,
		OwnerUserID: ownerUserID,
		Privacy:     PrivacyPrivate,
		Locale:      locale,
		Timezone:    timezone,
		CreatedAt:   now,
		UpdatedAt:   now,
		Version:     1,
	}
	return TreeAccess{
		Tree: tree,
		Membership: TreeMember{
			TreeID:     id,
			UserID:     ownerUserID,
			Role:       RoleOwner,
			Status:     MemberStatusActive,
			CreatedAt:  now,
			AcceptedAt: &acceptedAt,
		},
	}, nil
}

func ApplyUpdate(
	tree FamilyTree,
	expectedVersion int,
	values UpdateValues,
	now time.Time,
) (FamilyTree, error) {
	if expectedVersion <= 0 || tree.Version != expectedVersion {
		return FamilyTree{}, ErrTreeVersionConflict
	}
	if tree.DeletedAt != nil {
		return FamilyTree{}, ErrTreeNotFound
	}
	name := tree.Name
	description := tree.Description
	locale := tree.Locale
	timezone := tree.Timezone
	if values.Name != nil {
		name = *values.Name
	}
	if values.Description != nil {
		description = *values.Description
	}
	if values.Locale != nil {
		locale = *values.Locale
	}
	if values.Timezone != nil {
		timezone = *values.Timezone
	}
	var err error
	name, description, locale, timezone, err = normalizeValues(
		name,
		description,
		locale,
		timezone,
	)
	if err != nil {
		return FamilyTree{}, err
	}
	if values.Name == nil && values.Description == nil &&
		values.Locale == nil && values.Timezone == nil {
		return FamilyTree{}, fmt.Errorf("%w: no fields to update", ErrInvalidTree)
	}
	tree.Name = name
	tree.Description = description
	tree.Locale = locale
	tree.Timezone = timezone
	tree.UpdatedAt = now
	tree.Version++
	return tree, nil
}

func (a TreeAccess) CanView() bool {
	return a.Membership.Status == MemberStatusActive
}

func (a TreeAccess) CanEditSettings() bool {
	return a.CanView() && a.Membership.Role == RoleOwner
}

func normalizeValues(
	name string,
	description string,
	locale string,
	timezone string,
) (string, string, string, string, error) {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	locale = strings.TrimSpace(locale)
	timezone = strings.TrimSpace(timezone)
	if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > maxNameRunes ||
		utf8.RuneCountInString(description) > maxDescriptionRunes ||
		len(locale) > maxLocaleBytes || !localePattern.MatchString(locale) ||
		len(timezone) < 1 || len(timezone) > maxTimezoneBytes {
		return "", "", "", "", ErrInvalidTree
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return "", "", "", "", fmt.Errorf("%w: timezone is invalid", ErrInvalidTree)
	}
	return name, description, locale, timezone, nil
}
