package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	persondomain "github.com/ZheglY/family_tree_app/internal/features/persons/domain"
	treedomain "github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	"github.com/google/uuid"
)

const (
	defaultListLimit = 50
	maxListLimit     = 100
	maxQueryRunes    = 200
)

type Repository interface {
	CreateEditable(context.Context, uuid.UUID, persondomain.Card) error
	ListAccessible(context.Context, ListFilter) ([]persondomain.Card, error)
	GetAccessible(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, bool) (persondomain.Card, error)
	UpdateEditable(context.Context, uuid.UUID, persondomain.Card) error
	SoftDeleteEditable(context.Context, AuditMutation) error
	RestoreEditable(context.Context, AuditMutation) (persondomain.Card, error)
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
	Values      persondomain.CreateValues
}

type UpdateCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	PersonID    uuid.UUID
	Version     int
	Values      persondomain.UpdateValues
}

type MutationCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	PersonID    uuid.UUID
	Version     int
	RequestID   string
	IPAddress   string
}

type ListCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	Query       string
	LifeStatus  string
	HasMedia    *bool
	Cursor      string
	Limit       int
}

type ListFilter struct {
	ActorUserID   uuid.UUID
	TreeID        uuid.UUID
	Query         string
	LifeStatus    string
	HasMedia      *bool
	AfterName     string
	AfterPersonID uuid.UUID
	Limit         int
}

type AuditMutation struct {
	AuditID     uuid.UUID
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	PersonID    uuid.UUID
	Version     int
	OccurredAt  time.Time
	RequestID   string
	IPAddress   string
}

type Result struct {
	Card       persondomain.Card
	Membership treedomain.TreeMember
}

type ListResult struct {
	Items      []persondomain.Card
	Membership treedomain.TreeMember
	NextCursor string
}

type listCursor struct {
	Name     string    `json:"n"`
	PersonID uuid.UUID `json:"id"`
}

func (s *Service) Create(ctx context.Context, command CreateCommand) (Result, error) {
	treeAccess, err := s.editableTree(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return Result{}, err
	}
	card, err := persondomain.New(
		s.newID(),
		s.newID(),
		command.TreeID,
		command.ActorUserID,
		command.Values,
		s.now(),
	)
	if err != nil {
		return Result{}, err
	}
	if err := s.repository.CreateEditable(ctx, command.ActorUserID, card); err != nil {
		return Result{}, err
	}
	return Result{Card: card, Membership: treeAccess.Membership}, nil
}

func (s *Service) Get(
	ctx context.Context,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
	personID uuid.UUID,
) (Result, error) {
	treeAccess, err := s.readableTree(ctx, actorUserID, treeID)
	if err != nil {
		return Result{}, err
	}
	card, err := s.repository.GetAccessible(ctx, treeID, personID, actorUserID, false)
	if err != nil {
		return Result{}, err
	}
	return Result{Card: card, Membership: treeAccess.Membership}, nil
}

func (s *Service) List(ctx context.Context, command ListCommand) (ListResult, error) {
	treeAccess, err := s.readableTree(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return ListResult{}, err
	}
	filter, err := listFilter(command)
	if err != nil {
		return ListResult{}, err
	}
	cards, err := s.repository.ListAccessible(ctx, filter)
	if err != nil {
		return ListResult{}, err
	}
	result := ListResult{Items: cards, Membership: treeAccess.Membership}
	if len(cards) > commandLimit(command.Limit) {
		result.Items = cards[:commandLimit(command.Limit)]
		last := result.Items[len(result.Items)-1]
		result.NextCursor, err = encodeCursor(listCursor{
			Name:     last.PreferredName.FullText,
			PersonID: last.Person.ID,
		})
		if err != nil {
			return ListResult{}, err
		}
	}
	return result, nil
}

func (s *Service) Update(ctx context.Context, command UpdateCommand) (Result, error) {
	treeAccess, err := s.editableTree(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return Result{}, err
	}
	card, err := s.repository.GetAccessible(
		ctx,
		command.TreeID,
		command.PersonID,
		command.ActorUserID,
		false,
	)
	if err != nil {
		return Result{}, err
	}
	updated, err := persondomain.ApplyUpdate(
		card,
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
	return Result{Card: updated, Membership: treeAccess.Membership}, nil
}

func (s *Service) Delete(ctx context.Context, command MutationCommand) error {
	if _, err := s.editableTree(ctx, command.ActorUserID, command.TreeID); err != nil {
		return err
	}
	card, err := s.repository.GetAccessible(
		ctx,
		command.TreeID,
		command.PersonID,
		command.ActorUserID,
		false,
	)
	if err != nil {
		return err
	}
	if command.Version <= 0 || card.Person.Version != command.Version {
		return persondomain.ErrPersonVersionConflict
	}
	return s.repository.SoftDeleteEditable(ctx, s.auditMutation(command))
}

func (s *Service) Restore(ctx context.Context, command MutationCommand) (Result, error) {
	treeAccess, err := s.editableTree(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return Result{}, err
	}
	card, err := s.repository.GetAccessible(
		ctx,
		command.TreeID,
		command.PersonID,
		command.ActorUserID,
		true,
	)
	if err != nil {
		return Result{}, err
	}
	if card.Person.DeletedAt == nil {
		return Result{}, persondomain.ErrPersonNotDeleted
	}
	if command.Version <= 0 || card.Person.Version != command.Version {
		return Result{}, persondomain.ErrPersonVersionConflict
	}
	restored, err := s.repository.RestoreEditable(ctx, s.auditMutation(command))
	if err != nil {
		return Result{}, err
	}
	return Result{Card: restored, Membership: treeAccess.Membership}, nil
}

func (s *Service) readableTree(
	ctx context.Context,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
) (treedomain.TreeAccess, error) {
	if actorUserID == uuid.Nil || treeID == uuid.Nil {
		return treedomain.TreeAccess{}, persondomain.ErrPersonNotFound
	}
	access, err := s.treeRepository.GetAccessible(ctx, treeID, actorUserID, false)
	if errors.Is(err, treedomain.ErrTreeNotFound) {
		return treedomain.TreeAccess{}, persondomain.ErrPersonNotFound
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
		return treedomain.TreeAccess{}, persondomain.ErrPersonAccessDenied
	}
	return access, nil
}

func (s *Service) auditMutation(command MutationCommand) AuditMutation {
	return AuditMutation{
		AuditID:     s.newID(),
		ActorUserID: command.ActorUserID,
		TreeID:      command.TreeID,
		PersonID:    command.PersonID,
		Version:     command.Version,
		OccurredAt:  s.now(),
		RequestID:   command.RequestID,
		IPAddress:   command.IPAddress,
	}
}

func listFilter(command ListCommand) (ListFilter, error) {
	limit := commandLimit(command.Limit)
	if command.Limit < 0 || command.Limit > maxListLimit || len([]rune(command.Query)) > maxQueryRunes {
		return ListFilter{}, persondomain.ErrInvalidPerson
	}
	if command.LifeStatus != "" && !persondomain.IsLifeStatus(command.LifeStatus) {
		return ListFilter{}, persondomain.ErrInvalidPerson
	}
	filter := ListFilter{
		ActorUserID: command.ActorUserID,
		TreeID:      command.TreeID,
		Query:       strings.TrimSpace(command.Query),
		LifeStatus:  command.LifeStatus,
		HasMedia:    command.HasMedia,
		Limit:       limit + 1,
	}
	if command.Cursor == "" {
		return filter, nil
	}
	cursor, err := decodeCursor(command.Cursor)
	if err != nil {
		return ListFilter{}, err
	}
	filter.AfterName = cursor.Name
	filter.AfterPersonID = cursor.PersonID
	return filter, nil
}

func commandLimit(value int) int {
	if value == 0 {
		return defaultListLimit
	}
	return value
}

func encodeCursor(cursor listCursor) (string, error) {
	body, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func decodeCursor(value string) (listCursor, error) {
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return listCursor{}, persondomain.ErrInvalidListCursor
	}
	var cursor listCursor
	if err := json.Unmarshal(body, &cursor); err != nil ||
		strings.TrimSpace(cursor.Name) == "" || cursor.PersonID == uuid.Nil {
		return listCursor{}, persondomain.ErrInvalidListCursor
	}
	return cursor, nil
}
