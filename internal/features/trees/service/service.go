package service

import (
	"context"
	"time"

	"github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	"github.com/google/uuid"
)

const (
	defaultLocale   = "ru-RU"
	defaultTimezone = "UTC"
)

type Repository interface {
	CreateWithOwner(context.Context, domain.TreeAccess) error
	ListAccessible(context.Context, uuid.UUID) ([]domain.TreeAccess, error)
	GetAccessible(context.Context, uuid.UUID, uuid.UUID, bool) (domain.TreeAccess, error)
	UpdateOwned(context.Context, uuid.UUID, domain.FamilyTree) (domain.FamilyTree, error)
	SoftDeleteOwned(context.Context, AuditMutation) error
	RestoreOwned(context.Context, AuditMutation) (domain.FamilyTree, error)
}

type Service struct {
	repository Repository
	newID      func() uuid.UUID
	now        func() time.Time
}

func New(repository Repository) *Service {
	return &Service{
		repository: repository,
		newID:      uuid.New,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

type CreateCommand struct {
	ActorUserID uuid.UUID
	Name        string
	Description string
	Locale      string
	Timezone    string
}

type UpdateCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	Version     int
	Name        *string
	Description *string
	Locale      *string
	Timezone    *string
}

type MutationCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	Version     int
	RequestID   string
	IPAddress   string
}

type AuditMutation struct {
	AuditID     uuid.UUID
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	Version     int
	OccurredAt  time.Time
	RequestID   string
	IPAddress   string
}

func (s *Service) Create(
	ctx context.Context,
	command CreateCommand,
) (domain.TreeAccess, error) {
	locale := command.Locale
	if locale == "" {
		locale = defaultLocale
	}
	timezone := command.Timezone
	if timezone == "" {
		timezone = defaultTimezone
	}
	access, err := domain.NewFamilyTree(
		s.newID(),
		command.ActorUserID,
		domain.CreateValues{
			Name:        command.Name,
			Description: command.Description,
			Locale:      locale,
			Timezone:    timezone,
		},
		s.now(),
	)
	if err != nil {
		return domain.TreeAccess{}, err
	}
	if err := s.repository.CreateWithOwner(ctx, access); err != nil {
		return domain.TreeAccess{}, err
	}
	return access, nil
}

func (s *Service) List(
	ctx context.Context,
	actorUserID uuid.UUID,
) ([]domain.TreeAccess, error) {
	if actorUserID == uuid.Nil {
		return nil, domain.ErrTreeNotFound
	}
	return s.repository.ListAccessible(ctx, actorUserID)
}

func (s *Service) Get(
	ctx context.Context,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
) (domain.TreeAccess, error) {
	if actorUserID == uuid.Nil || treeID == uuid.Nil {
		return domain.TreeAccess{}, domain.ErrTreeNotFound
	}
	return s.repository.GetAccessible(ctx, treeID, actorUserID, false)
}

func (s *Service) Update(
	ctx context.Context,
	command UpdateCommand,
) (domain.TreeAccess, error) {
	access, err := s.Get(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return domain.TreeAccess{}, err
	}
	if !access.CanEditSettings() {
		return domain.TreeAccess{}, domain.ErrTreeAccessDenied
	}
	updated, err := domain.ApplyUpdate(
		access.Tree,
		command.Version,
		domain.UpdateValues{
			Name:        command.Name,
			Description: command.Description,
			Locale:      command.Locale,
			Timezone:    command.Timezone,
		},
		s.now(),
	)
	if err != nil {
		return domain.TreeAccess{}, err
	}
	updated, err = s.repository.UpdateOwned(ctx, command.ActorUserID, updated)
	if err != nil {
		return domain.TreeAccess{}, err
	}
	access.Tree = updated
	return access, nil
}

func (s *Service) Delete(ctx context.Context, command MutationCommand) error {
	access, err := s.Get(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return err
	}
	if !access.CanEditSettings() {
		return domain.ErrTreeAccessDenied
	}
	if command.Version <= 0 || access.Tree.Version != command.Version {
		return domain.ErrTreeVersionConflict
	}
	return s.repository.SoftDeleteOwned(ctx, s.auditMutation(command))
}

func (s *Service) Restore(
	ctx context.Context,
	command MutationCommand,
) (domain.TreeAccess, error) {
	access, err := s.repository.GetAccessible(
		ctx,
		command.TreeID,
		command.ActorUserID,
		true,
	)
	if err != nil {
		return domain.TreeAccess{}, err
	}
	if !access.CanEditSettings() {
		return domain.TreeAccess{}, domain.ErrTreeAccessDenied
	}
	if access.Tree.DeletedAt == nil {
		return domain.TreeAccess{}, domain.ErrTreeNotDeleted
	}
	if command.Version <= 0 || access.Tree.Version != command.Version {
		return domain.TreeAccess{}, domain.ErrTreeVersionConflict
	}
	restored, err := s.repository.RestoreOwned(ctx, s.auditMutation(command))
	if err != nil {
		return domain.TreeAccess{}, err
	}
	access.Tree = restored
	return access, nil
}

func (s *Service) auditMutation(command MutationCommand) AuditMutation {
	return AuditMutation{
		AuditID:     s.newID(),
		ActorUserID: command.ActorUserID,
		TreeID:      command.TreeID,
		Version:     command.Version,
		OccurredAt:  s.now(),
		RequestID:   command.RequestID,
		IPAddress:   command.IPAddress,
	}
}
