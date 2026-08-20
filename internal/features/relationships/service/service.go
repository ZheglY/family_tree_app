package service

import (
	"context"
	"errors"
	"time"

	"github.com/ZheglY/family_tree_app/internal/features/relationships/domain"
	treedomain "github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	"github.com/google/uuid"
)

const (
	MaxGraphDepth = 6
	MaxGraphNodes = 500
)

type Repository interface {
	CreateAcyclicEditable(context.Context, uuid.UUID, domain.ParentChildRelation) error
	GetAccessible(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (domain.ParentChildRelation, error)
	UpdateEditable(context.Context, uuid.UUID, domain.ParentChildRelation) error
	SoftDeleteEditable(context.Context, AuditMutation) error
	LoadGraphAccessible(context.Context, GraphFilter) (domain.Graph, error)
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
	RelationID  uuid.UUID
	Version     int
	Values      domain.UpdateValues
}

type MutationCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	RelationID  uuid.UUID
	Version     int
	RequestID   string
	IPAddress   string
}

type GraphCommand struct {
	ActorUserID      uuid.UUID
	TreeID           uuid.UUID
	CenterPersonID   uuid.UUID
	AncestorsDepth   int
	DescendantsDepth int
	IncludePartners  bool
}

type GraphFilter struct {
	ActorUserID      uuid.UUID
	TreeID           uuid.UUID
	CenterPersonID   uuid.UUID
	AncestorsDepth   int
	DescendantsDepth int
	MaxNodes         int
}

type AuditMutation struct {
	AuditID     uuid.UUID
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	RelationID  uuid.UUID
	Version     int
	OccurredAt  time.Time
	RequestID   string
	IPAddress   string
}

type Result struct {
	Relation   domain.ParentChildRelation
	Membership treedomain.TreeMember
}

type GraphResult struct {
	Graph           domain.Graph
	Membership      treedomain.TreeMember
	IncludePartners bool
}

func (s *Service) Create(ctx context.Context, command CreateCommand) (Result, error) {
	treeAccess, err := s.editableTree(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return Result{}, err
	}
	relation, err := domain.New(
		s.newID(),
		command.TreeID,
		command.ActorUserID,
		command.Values,
		s.now(),
	)
	if err != nil {
		return Result{}, err
	}
	if err := s.repository.CreateAcyclicEditable(ctx, command.ActorUserID, relation); err != nil {
		return Result{}, err
	}
	return Result{Relation: relation, Membership: treeAccess.Membership}, nil
}

func (s *Service) Get(
	ctx context.Context,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
	relationID uuid.UUID,
) (Result, error) {
	treeAccess, err := s.readableTree(ctx, actorUserID, treeID)
	if err != nil {
		return Result{}, err
	}
	relation, err := s.repository.GetAccessible(ctx, treeID, relationID, actorUserID)
	if err != nil {
		return Result{}, err
	}
	return Result{Relation: relation, Membership: treeAccess.Membership}, nil
}

func (s *Service) Update(ctx context.Context, command UpdateCommand) (Result, error) {
	treeAccess, err := s.editableTree(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return Result{}, err
	}
	relation, err := s.repository.GetAccessible(
		ctx,
		command.TreeID,
		command.RelationID,
		command.ActorUserID,
	)
	if err != nil {
		return Result{}, err
	}
	updated, err := domain.ApplyUpdate(relation, command.Version, command.Values, s.now())
	if err != nil {
		return Result{}, err
	}
	if err := s.repository.UpdateEditable(ctx, command.ActorUserID, updated); err != nil {
		return Result{}, err
	}
	return Result{Relation: updated, Membership: treeAccess.Membership}, nil
}

func (s *Service) Delete(ctx context.Context, command MutationCommand) error {
	if _, err := s.editableTree(ctx, command.ActorUserID, command.TreeID); err != nil {
		return err
	}
	relation, err := s.repository.GetAccessible(
		ctx,
		command.TreeID,
		command.RelationID,
		command.ActorUserID,
	)
	if err != nil {
		return err
	}
	if command.Version <= 0 || relation.Version != command.Version {
		return domain.ErrRelationVersionConflict
	}
	return s.repository.SoftDeleteEditable(ctx, s.auditMutation(command))
}

func (s *Service) Graph(ctx context.Context, command GraphCommand) (GraphResult, error) {
	treeAccess, err := s.readableTree(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return GraphResult{}, err
	}
	if command.CenterPersonID == uuid.Nil || command.AncestorsDepth < 0 ||
		command.DescendantsDepth < 0 || command.AncestorsDepth > MaxGraphDepth ||
		command.DescendantsDepth > MaxGraphDepth {
		return GraphResult{}, domain.ErrInvalidRelation
	}
	graph, err := s.repository.LoadGraphAccessible(ctx, GraphFilter{
		ActorUserID:      command.ActorUserID,
		TreeID:           command.TreeID,
		CenterPersonID:   command.CenterPersonID,
		AncestorsDepth:   command.AncestorsDepth,
		DescendantsDepth: command.DescendantsDepth,
		MaxNodes:         MaxGraphNodes,
	})
	if err != nil {
		return GraphResult{}, err
	}
	return GraphResult{
		Graph:           graph,
		Membership:      treeAccess.Membership,
		IncludePartners: command.IncludePartners,
	}, nil
}

func (s *Service) readableTree(
	ctx context.Context,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
) (treedomain.TreeAccess, error) {
	if actorUserID == uuid.Nil || treeID == uuid.Nil {
		return treedomain.TreeAccess{}, domain.ErrRelationNotFound
	}
	access, err := s.treeRepository.GetAccessible(ctx, treeID, actorUserID, false)
	if errors.Is(err, treedomain.ErrTreeNotFound) {
		return treedomain.TreeAccess{}, domain.ErrRelationNotFound
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
		return treedomain.TreeAccess{}, domain.ErrRelationAccessDenied
	}
	return access, nil
}

func (s *Service) auditMutation(command MutationCommand) AuditMutation {
	return AuditMutation{
		AuditID:     s.newID(),
		ActorUserID: command.ActorUserID,
		TreeID:      command.TreeID,
		RelationID:  command.RelationID,
		Version:     command.Version,
		OccurredAt:  s.now(),
		RequestID:   command.RequestID,
		IPAddress:   command.IPAddress,
	}
}
