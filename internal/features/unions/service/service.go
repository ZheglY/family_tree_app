package service

import (
	"context"
	"errors"
	"time"

	treedomain "github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	"github.com/ZheglY/family_tree_app/internal/features/unions/domain"
	"github.com/google/uuid"
)

type Repository interface {
	CreateWithMembersEditable(context.Context, uuid.UUID, domain.Aggregate) error
	GetAccessible(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (domain.Aggregate, error)
	UpdateEditable(context.Context, uuid.UUID, domain.FamilyUnion) error
	SoftDeleteEditable(context.Context, AuditMutation) error
	AddMemberEditable(context.Context, uuid.UUID, domain.UnionMember) (domain.Aggregate, error)
	RemoveMemberEditable(context.Context, MemberMutation) (domain.Aggregate, error)
}

type TreeRepository interface {
	GetAccessible(context.Context, uuid.UUID, uuid.UUID, bool) (treedomain.TreeAccess, error)
}

type Service struct {
	repository     Repository
	treeRepository TreeRepository
	newID          func() uuid.UUID
	now            func() time.Time
}

func New(repository Repository, treeRepository TreeRepository) *Service {
	return &Service{
		repository:     repository,
		treeRepository: treeRepository,
		newID:          uuid.New,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

type CreateCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	Values      domain.CreateValues
}

type UpdateCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	UnionID     uuid.UUID
	Version     int
	Values      domain.UpdateValues
}

type MutationCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	UnionID     uuid.UUID
	Version     int
	RequestID   string
	IPAddress   string
}

type AddMemberCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	UnionID     uuid.UUID
	PersonID    uuid.UUID
	Role        string
}

type RemoveMemberCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	UnionID     uuid.UUID
	PersonID    uuid.UUID
}

type AuditMutation struct {
	AuditID     uuid.UUID
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	UnionID     uuid.UUID
	Version     int
	OccurredAt  time.Time
	RequestID   string
	IPAddress   string
}

type MemberMutation struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	UnionID     uuid.UUID
	PersonID    uuid.UUID
	OccurredAt  time.Time
}

type Result struct {
	Aggregate  domain.Aggregate
	Membership treedomain.TreeMember
}

func (s *Service) Create(ctx context.Context, command CreateCommand) (Result, error) {
	treeAccess, err := s.editableTree(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return Result{}, err
	}
	aggregate, err := domain.New(
		s.newID(),
		command.TreeID,
		command.ActorUserID,
		command.Values,
		s.now(),
	)
	if err != nil {
		return Result{}, err
	}
	if err := s.repository.CreateWithMembersEditable(
		ctx,
		command.ActorUserID,
		aggregate,
	); err != nil {
		return Result{}, err
	}
	return Result{Aggregate: aggregate, Membership: treeAccess.Membership}, nil
}

func (s *Service) Get(
	ctx context.Context,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
	unionID uuid.UUID,
) (Result, error) {
	treeAccess, err := s.readableTree(ctx, actorUserID, treeID)
	if err != nil {
		return Result{}, err
	}
	aggregate, err := s.repository.GetAccessible(ctx, treeID, unionID, actorUserID)
	if err != nil {
		return Result{}, err
	}
	return Result{Aggregate: aggregate, Membership: treeAccess.Membership}, nil
}

func (s *Service) Update(ctx context.Context, command UpdateCommand) (Result, error) {
	treeAccess, err := s.editableTree(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return Result{}, err
	}
	aggregate, err := s.repository.GetAccessible(
		ctx,
		command.TreeID,
		command.UnionID,
		command.ActorUserID,
	)
	if err != nil {
		return Result{}, err
	}
	updated, err := domain.ApplyUpdate(
		aggregate.Union,
		command.Version,
		command.ActorUserID,
		command.Values,
		s.now(),
	)
	if err != nil {
		return Result{}, err
	}
	if err := s.repository.UpdateEditable(ctx, command.ActorUserID, updated); err != nil {
		return Result{}, err
	}
	aggregate.Union = updated
	return Result{Aggregate: aggregate, Membership: treeAccess.Membership}, nil
}

func (s *Service) Delete(ctx context.Context, command MutationCommand) error {
	if _, err := s.editableTree(ctx, command.ActorUserID, command.TreeID); err != nil {
		return err
	}
	aggregate, err := s.repository.GetAccessible(
		ctx,
		command.TreeID,
		command.UnionID,
		command.ActorUserID,
	)
	if err != nil {
		return err
	}
	if command.Version <= 0 || aggregate.Union.Version != command.Version {
		return domain.ErrUnionVersionConflict
	}
	return s.repository.SoftDeleteEditable(ctx, s.auditMutation(command))
}

func (s *Service) AddMember(ctx context.Context, command AddMemberCommand) (Result, error) {
	treeAccess, err := s.editableTree(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return Result{}, err
	}
	member, err := domain.NewMember(
		command.UnionID,
		command.TreeID,
		domain.MemberValues{PersonID: command.PersonID, Role: command.Role},
		s.now(),
	)
	if err != nil {
		return Result{}, err
	}
	aggregate, err := s.repository.AddMemberEditable(ctx, command.ActorUserID, member)
	if err != nil {
		return Result{}, err
	}
	return Result{Aggregate: aggregate, Membership: treeAccess.Membership}, nil
}

func (s *Service) RemoveMember(
	ctx context.Context,
	command RemoveMemberCommand,
) (Result, error) {
	treeAccess, err := s.editableTree(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return Result{}, err
	}
	aggregate, err := s.repository.RemoveMemberEditable(
		ctx,
		MemberMutation{
			ActorUserID: command.ActorUserID,
			TreeID:      command.TreeID,
			UnionID:     command.UnionID,
			PersonID:    command.PersonID,
			OccurredAt:  s.now(),
		},
	)
	if err != nil {
		return Result{}, err
	}
	return Result{Aggregate: aggregate, Membership: treeAccess.Membership}, nil
}

func (s *Service) readableTree(
	ctx context.Context,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
) (treedomain.TreeAccess, error) {
	if actorUserID == uuid.Nil || treeID == uuid.Nil {
		return treedomain.TreeAccess{}, domain.ErrUnionNotFound
	}
	access, err := s.treeRepository.GetAccessible(ctx, treeID, actorUserID, false)
	if errors.Is(err, treedomain.ErrTreeNotFound) {
		return treedomain.TreeAccess{}, domain.ErrUnionNotFound
	}
	if err != nil {
		return treedomain.TreeAccess{}, err
	}
	return access, nil
}

func (s *Service) editableTree(
	ctx context.Context,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
) (treedomain.TreeAccess, error) {
	access, err := s.readableTree(ctx, actorUserID, treeID)
	if err != nil {
		return treedomain.TreeAccess{}, err
	}
	if !access.CanEditData() {
		return treedomain.TreeAccess{}, domain.ErrUnionAccessDenied
	}
	return access, nil
}

func (s *Service) auditMutation(command MutationCommand) AuditMutation {
	return AuditMutation{
		AuditID:     s.newID(),
		ActorUserID: command.ActorUserID,
		TreeID:      command.TreeID,
		UnionID:     command.UnionID,
		Version:     command.Version,
		OccurredAt:  s.now(),
		RequestID:   command.RequestID,
		IPAddress:   command.IPAddress,
	}
}
