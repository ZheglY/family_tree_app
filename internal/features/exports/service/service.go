package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/storage"
	"github.com/ZheglY/family_tree_app/internal/features/exports/domain"
	treedomain "github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	"github.com/google/uuid"
)

const (
	defaultListLimit = 20
	maxListLimit     = 100
)

type Repository interface {
	CreateAccessible(context.Context, domain.Export, AuditContext) (domain.Export, bool, error)
	GetAccessible(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (domain.Export, error)
	ListAccessible(context.Context, ListFilter) ([]domain.Export, error)
	ExpireAccessible(context.Context, MutationContext) error
	RecordDownload(context.Context, DownloadAudit) error
}

type TreeRepository interface {
	GetAccessible(context.Context, uuid.UUID, uuid.UUID, bool) (treedomain.TreeAccess, error)
}

type Service struct {
	repository     Repository
	treeRepository TreeRepository
	objectStore    storage.ObjectStore
	newID          func() uuid.UUID
	now            func() time.Time
}

func New(
	repository Repository,
	treeRepository TreeRepository,
	objectStore storage.ObjectStore,
) *Service {
	return &Service{
		repository:     repository,
		treeRepository: treeRepository,
		objectStore:    objectStore,
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
	RequestID   string
	IPAddress   string
}

type ListCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	Cursor      string
	Limit       int
}

type ListFilter struct {
	ActorUserID    uuid.UUID
	TreeID         uuid.UUID
	BeforeCreated  time.Time
	BeforeExportID uuid.UUID
	Limit          int
}

type MutationCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	ExportID    uuid.UUID
	RequestID   string
	IPAddress   string
}

type AuditContext struct {
	AuditID   uuid.UUID
	RequestID string
	IPAddress string
}

type MutationContext struct {
	AuditID     uuid.UUID
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	ExportID    uuid.UUID
	OccurredAt  time.Time
	RequestID   string
	IPAddress   string
}

type DownloadAudit struct {
	AuditID     uuid.UUID
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	ExportID    uuid.UUID
	OccurredAt  time.Time
	RequestID   string
	IPAddress   string
}

type Result struct {
	Export     domain.Export
	Membership treedomain.TreeMember
	Download   *storage.PresignedRequest
}

type CreateResult struct {
	Result
	Created bool
}

type ListResult struct {
	Items      []domain.Export
	Membership treedomain.TreeMember
	NextCursor string
}

type listCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ExportID  uuid.UUID `json:"export_id"`
}

func (service *Service) Create(
	ctx context.Context,
	command CreateCommand,
) (CreateResult, error) {
	access, err := service.readableTree(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return CreateResult{}, err
	}
	if !access.CanEditData() {
		return CreateResult{}, domain.ErrExportAccessDenied
	}
	export, err := domain.New(
		service.newID(),
		command.TreeID,
		command.ActorUserID,
		command.Values,
		service.now(),
	)
	if err != nil {
		return CreateResult{}, err
	}
	export, created, err := service.repository.CreateAccessible(ctx, export, AuditContext{
		AuditID:   service.newID(),
		RequestID: command.RequestID,
		IPAddress: command.IPAddress,
	})
	if err != nil {
		return CreateResult{}, err
	}
	return CreateResult{
		Result:  Result{Export: export, Membership: access.Membership},
		Created: created,
	}, nil
}

func (service *Service) Get(
	ctx context.Context,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
	exportID uuid.UUID,
) (Result, error) {
	access, err := service.readableTree(ctx, actorUserID, treeID)
	if err != nil {
		return Result{}, err
	}
	export, err := service.repository.GetAccessible(ctx, treeID, exportID, actorUserID)
	if err != nil {
		return Result{}, err
	}
	return Result{Export: export, Membership: access.Membership}, nil
}

func (service *Service) List(
	ctx context.Context,
	command ListCommand,
) (ListResult, error) {
	access, err := service.readableTree(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return ListResult{}, err
	}
	filter, pageSize, err := listFilter(command)
	if err != nil {
		return ListResult{}, err
	}
	items, err := service.repository.ListAccessible(ctx, filter)
	if err != nil {
		return ListResult{}, err
	}
	more := len(items) > pageSize
	if more {
		items = items[:pageSize]
	}
	result := ListResult{Items: items, Membership: access.Membership}
	if more {
		last := items[len(items)-1]
		result.NextCursor, err = encodeCursor(listCursor{
			CreatedAt: last.CreatedAt,
			ExportID:  last.ID,
		})
		if err != nil {
			return ListResult{}, err
		}
	}
	return result, nil
}

func (service *Service) Download(
	ctx context.Context,
	command MutationCommand,
) (Result, error) {
	result, err := service.Get(ctx, command.ActorUserID, command.TreeID, command.ExportID)
	if err != nil {
		return Result{}, err
	}
	if !domain.CanDownload(result.Export, service.now()) {
		return Result{}, domain.ErrExportResultUnavailable
	}
	download, err := service.objectStore.PresignDownload(
		ctx,
		result.Export.ResultObjectKey,
		domain.ResultFilename(result.Export),
	)
	if err != nil {
		return Result{}, err
	}
	if err := service.repository.RecordDownload(ctx, DownloadAudit{
		AuditID:     service.newID(),
		ActorUserID: command.ActorUserID,
		TreeID:      command.TreeID,
		ExportID:    command.ExportID,
		OccurredAt:  service.now(),
		RequestID:   command.RequestID,
		IPAddress:   command.IPAddress,
	}); err != nil {
		return Result{}, err
	}
	result.Download = &download
	return result, nil
}

func (service *Service) Delete(ctx context.Context, command MutationCommand) error {
	if _, err := service.readableTree(ctx, command.ActorUserID, command.TreeID); err != nil {
		return err
	}
	return service.repository.ExpireAccessible(ctx, MutationContext{
		AuditID:     service.newID(),
		ActorUserID: command.ActorUserID,
		TreeID:      command.TreeID,
		ExportID:    command.ExportID,
		OccurredAt:  service.now(),
		RequestID:   command.RequestID,
		IPAddress:   command.IPAddress,
	})
}

func (service *Service) readableTree(
	ctx context.Context,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
) (treedomain.TreeAccess, error) {
	if actorUserID == uuid.Nil || treeID == uuid.Nil {
		return treedomain.TreeAccess{}, domain.ErrExportNotFound
	}
	access, err := service.treeRepository.GetAccessible(ctx, treeID, actorUserID, false)
	if errors.Is(err, treedomain.ErrTreeNotFound) {
		return treedomain.TreeAccess{}, domain.ErrExportNotFound
	}
	if err != nil {
		return treedomain.TreeAccess{}, err
	}
	return access, nil
}

func listFilter(command ListCommand) (ListFilter, int, error) {
	limit := command.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	if limit < 1 || limit > maxListLimit {
		return ListFilter{}, 0, domain.ErrInvalidExport
	}
	cursor, err := decodeCursor(command.Cursor)
	if err != nil {
		return ListFilter{}, 0, domain.ErrInvalidExport
	}
	return ListFilter{
		ActorUserID:    command.ActorUserID,
		TreeID:         command.TreeID,
		BeforeCreated:  cursor.CreatedAt,
		BeforeExportID: cursor.ExportID,
		Limit:          limit + 1,
	}, limit, nil
}

func encodeCursor(cursor listCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode export cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeCursor(value string) (listCursor, error) {
	if value == "" {
		return listCursor{}, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return listCursor{}, err
	}
	var cursor listCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return listCursor{}, err
	}
	if cursor.CreatedAt.IsZero() || cursor.ExportID == uuid.Nil {
		return listCursor{}, domain.ErrInvalidExport
	}
	return cursor, nil
}
