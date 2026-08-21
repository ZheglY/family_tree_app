package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/storage"
	"github.com/ZheglY/family_tree_app/internal/features/media/domain"
	treedomain "github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	"github.com/google/uuid"
)

const (
	defaultListLimit = 20
	maxListLimit     = 100
)

type Repository interface {
	CreateIntentEditable(context.Context, uuid.UUID, domain.MediaAsset) (domain.MediaAsset, bool, error)
	GetAccessible(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (domain.MediaAsset, error)
	ListAccessible(context.Context, ListFilter) ([]domain.MediaAsset, error)
	ListVariantsAccessible(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID) (map[uuid.UUID][]domain.MediaVariant, error)
	CompleteUploadEditable(context.Context, uuid.UUID, domain.MediaAsset) error
	UpdateEditable(context.Context, uuid.UUID, domain.MediaAsset) error
	SoftDeleteEditable(context.Context, AuditMutation) error
	AttachToPersonEditable(context.Context, uuid.UUID, domain.PersonMedia) error
	DetachFromPersonEditable(context.Context, AttachmentMutation) error
	SetPrimaryPersonMediaEditable(context.Context, PrimaryMediaMutation) (int, error)
}

type TreeRepository interface {
	GetAccessible(context.Context, uuid.UUID, uuid.UUID, bool) (treedomain.TreeAccess, error)
}

type Service struct {
	repository     Repository
	treeRepository TreeRepository
	objectStore    storage.ObjectStore
	maxUploadBytes int64
	newID          func() uuid.UUID
	now            func() time.Time
}

func New(
	repository Repository,
	treeRepository TreeRepository,
	objectStore storage.ObjectStore,
	maxUploadBytes int64,
) *Service {
	return &Service{
		repository:     repository,
		treeRepository: treeRepository,
		objectStore:    objectStore,
		maxUploadBytes: maxUploadBytes,
		newID:          uuid.New,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

type UploadIntentCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	Values      domain.CreateValues
}

type UpdateCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	MediaID     uuid.UUID
	Version     int
	Values      domain.UpdateValues
}

type MutationCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	MediaID     uuid.UUID
	Version     int
	RequestID   string
	IPAddress   string
}

type ListCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	Kind        string
	Status      string
	Cursor      string
	Limit       int
}

type ListFilter struct {
	ActorUserID   uuid.UUID
	TreeID        uuid.UUID
	Kind          string
	Status        string
	BeforeCreated time.Time
	BeforeMediaID uuid.UUID
	Limit         int
}

type AttachCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	PersonID    uuid.UUID
	MediaID     uuid.UUID
	Role        string
	SortOrder   int
}

type DetachCommand struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	PersonID    uuid.UUID
	MediaID     uuid.UUID
}

type SetPrimaryCommand struct {
	ActorUserID   uuid.UUID
	TreeID        uuid.UUID
	PersonID      uuid.UUID
	MediaID       uuid.UUID
	PersonVersion int
}

type AuditMutation struct {
	AuditID     uuid.UUID
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	MediaID     uuid.UUID
	Version     int
	OccurredAt  time.Time
	RequestID   string
	IPAddress   string
}

type AttachmentMutation struct {
	ActorUserID uuid.UUID
	TreeID      uuid.UUID
	PersonID    uuid.UUID
	MediaID     uuid.UUID
	OccurredAt  time.Time
}

type PrimaryMediaMutation struct {
	ActorUserID   uuid.UUID
	TreeID        uuid.UUID
	PersonID      uuid.UUID
	MediaID       uuid.UUID
	PersonVersion int
	OccurredAt    time.Time
}

type Result struct {
	Asset      domain.MediaAsset
	Membership treedomain.TreeMember
	Download   *storage.PresignedRequest
	Variants   []VariantResult
}

type VariantResult struct {
	Variant  domain.MediaVariant
	Download storage.PresignedRequest
}

type UploadIntentResult struct {
	Asset      domain.MediaAsset
	Membership treedomain.TreeMember
	Upload     *storage.PresignedRequest
	Created    bool
}

type ListResult struct {
	Items      []Result
	Membership treedomain.TreeMember
	NextCursor string
}

type AttachmentResult struct {
	Attachment domain.PersonMedia
	Asset      domain.MediaAsset
	Membership treedomain.TreeMember
}

type PrimaryMediaResult struct {
	PersonID      uuid.UUID
	MediaID       uuid.UUID
	PersonVersion int
	Membership    treedomain.TreeMember
}

type listCursor struct {
	CreatedAt time.Time `json:"created_at"`
	MediaID   uuid.UUID `json:"media_id"`
}

func (s *Service) CreateUploadIntent(
	ctx context.Context,
	command UploadIntentCommand,
) (UploadIntentResult, error) {
	treeAccess, err := s.editableTree(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return UploadIntentResult{}, err
	}
	mediaID := s.newID()
	asset, err := domain.New(
		mediaID,
		command.TreeID,
		command.ActorUserID,
		fmt.Sprintf("trees/%s/media/%s/original", command.TreeID, mediaID),
		command.Values,
		s.maxUploadBytes,
		s.now(),
	)
	if err != nil {
		return UploadIntentResult{}, err
	}
	asset, created, err := s.repository.CreateIntentEditable(ctx, command.ActorUserID, asset)
	if err != nil {
		return UploadIntentResult{}, err
	}
	result := UploadIntentResult{
		Asset:      asset,
		Membership: treeAccess.Membership,
		Created:    created,
	}
	if asset.Status != domain.StatusPending {
		return result, nil
	}
	upload, err := s.objectStore.PresignUpload(ctx, storage.UploadInput{
		ObjectKey:      asset.ObjectKey,
		ContentType:    asset.MIMEType,
		SizeBytes:      asset.SizeBytes,
		ChecksumSHA256: asset.ChecksumSHA256,
	})
	if err != nil {
		return UploadIntentResult{}, err
	}
	result.Upload = &upload
	return result, nil
}

func (s *Service) CompleteUpload(
	ctx context.Context,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
	mediaID uuid.UUID,
) (Result, error) {
	treeAccess, err := s.editableTree(ctx, actorUserID, treeID)
	if err != nil {
		return Result{}, err
	}
	asset, err := s.repository.GetAccessible(ctx, treeID, mediaID, actorUserID)
	if err != nil {
		return Result{}, err
	}
	if asset.Status != domain.StatusPending {
		if asset.Status == domain.StatusUploaded || asset.Status == domain.StatusProcessing ||
			domain.CanDownload(asset.Status) {
			return s.resultWithDownload(ctx, asset, treeAccess.Membership)
		}
		return Result{}, domain.ErrMediaStateConflict
	}
	object, err := s.objectStore.HeadObject(ctx, asset.ObjectKey)
	if errors.Is(err, storage.ErrObjectNotFound) {
		return Result{}, domain.ErrUploadedObjectNotFound
	}
	if err != nil {
		return Result{}, err
	}
	completed, err := domain.ApplyUploadCompletion(asset, domain.UploadedObject{
		MIMEType:       object.ContentType,
		SizeBytes:      object.SizeBytes,
		ChecksumSHA256: object.ChecksumSHA256,
		ETag:           object.ETag,
	}, s.now())
	if err != nil {
		return Result{}, err
	}
	if err := s.repository.CompleteUploadEditable(ctx, actorUserID, completed); err != nil {
		return Result{}, err
	}
	return s.resultWithDownload(ctx, completed, treeAccess.Membership)
}

func (s *Service) Get(
	ctx context.Context,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
	mediaID uuid.UUID,
) (Result, error) {
	treeAccess, err := s.readableTree(ctx, actorUserID, treeID)
	if err != nil {
		return Result{}, err
	}
	asset, err := s.repository.GetAccessible(ctx, treeID, mediaID, actorUserID)
	if err != nil {
		return Result{}, err
	}
	return s.resultWithDownload(ctx, asset, treeAccess.Membership)
}

func (s *Service) List(ctx context.Context, command ListCommand) (ListResult, error) {
	treeAccess, err := s.readableTree(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return ListResult{}, err
	}
	filter, pageSize, err := listFilter(command)
	if err != nil {
		return ListResult{}, err
	}
	assets, err := s.repository.ListAccessible(ctx, filter)
	if err != nil {
		return ListResult{}, err
	}
	more := len(assets) > pageSize
	if more {
		assets = assets[:pageSize]
	}
	result := ListResult{Membership: treeAccess.Membership, Items: make([]Result, 0, len(assets))}
	mediaIDs := make([]uuid.UUID, 0, len(assets))
	for _, asset := range assets {
		mediaIDs = append(mediaIDs, asset.ID)
	}
	variants, err := s.repository.ListVariantsAccessible(
		ctx,
		command.TreeID,
		command.ActorUserID,
		mediaIDs,
	)
	if err != nil {
		return ListResult{}, err
	}
	for _, asset := range assets {
		item, err := s.resultWithKnownVariants(ctx, asset, treeAccess.Membership, variants[asset.ID])
		if err != nil {
			return ListResult{}, err
		}
		result.Items = append(result.Items, item)
	}
	if more {
		last := assets[len(assets)-1]
		result.NextCursor, err = encodeCursor(listCursor{CreatedAt: last.CreatedAt, MediaID: last.ID})
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
	asset, err := s.repository.GetAccessible(ctx, command.TreeID, command.MediaID, command.ActorUserID)
	if err != nil {
		return Result{}, err
	}
	updated, err := domain.ApplyUpdate(asset, command.Version, command.Values, s.now())
	if err != nil {
		return Result{}, err
	}
	if err := s.repository.UpdateEditable(ctx, command.ActorUserID, updated); err != nil {
		return Result{}, err
	}
	return s.resultWithDownload(ctx, updated, treeAccess.Membership)
}

func (s *Service) Delete(ctx context.Context, command MutationCommand) error {
	if _, err := s.editableTree(ctx, command.ActorUserID, command.TreeID); err != nil {
		return err
	}
	asset, err := s.repository.GetAccessible(ctx, command.TreeID, command.MediaID, command.ActorUserID)
	if err != nil {
		return err
	}
	if command.Version <= 0 || asset.Version != command.Version {
		return domain.ErrMediaVersionConflict
	}
	return s.repository.SoftDeleteEditable(ctx, AuditMutation{
		AuditID:     s.newID(),
		ActorUserID: command.ActorUserID,
		TreeID:      command.TreeID,
		MediaID:     command.MediaID,
		Version:     command.Version,
		OccurredAt:  s.now(),
		RequestID:   command.RequestID,
		IPAddress:   command.IPAddress,
	})
}

func (s *Service) AttachToPerson(
	ctx context.Context,
	command AttachCommand,
) (AttachmentResult, error) {
	treeAccess, err := s.editableTree(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return AttachmentResult{}, err
	}
	attachment, err := domain.NewAttachment(
		command.TreeID,
		command.MediaID,
		command.ActorUserID,
		domain.AttachmentValues{
			PersonID:  command.PersonID,
			Role:      command.Role,
			SortOrder: command.SortOrder,
		},
		s.now(),
	)
	if err != nil {
		return AttachmentResult{}, err
	}
	if err := s.repository.AttachToPersonEditable(ctx, command.ActorUserID, attachment); err != nil {
		return AttachmentResult{}, err
	}
	asset, err := s.repository.GetAccessible(ctx, command.TreeID, command.MediaID, command.ActorUserID)
	if err != nil {
		return AttachmentResult{}, err
	}
	return AttachmentResult{Attachment: attachment, Asset: asset, Membership: treeAccess.Membership}, nil
}

func (s *Service) DetachFromPerson(ctx context.Context, command DetachCommand) error {
	if _, err := s.editableTree(ctx, command.ActorUserID, command.TreeID); err != nil {
		return err
	}
	return s.repository.DetachFromPersonEditable(ctx, AttachmentMutation{
		ActorUserID: command.ActorUserID,
		TreeID:      command.TreeID,
		PersonID:    command.PersonID,
		MediaID:     command.MediaID,
		OccurredAt:  s.now(),
	})
}

func (s *Service) SetPrimaryPersonMedia(
	ctx context.Context,
	command SetPrimaryCommand,
) (PrimaryMediaResult, error) {
	treeAccess, err := s.editableTree(ctx, command.ActorUserID, command.TreeID)
	if err != nil {
		return PrimaryMediaResult{}, err
	}
	newVersion, err := s.repository.SetPrimaryPersonMediaEditable(ctx, PrimaryMediaMutation{
		ActorUserID:   command.ActorUserID,
		TreeID:        command.TreeID,
		PersonID:      command.PersonID,
		MediaID:       command.MediaID,
		PersonVersion: command.PersonVersion,
		OccurredAt:    s.now(),
	})
	if err != nil {
		return PrimaryMediaResult{}, err
	}
	return PrimaryMediaResult{
		PersonID:      command.PersonID,
		MediaID:       command.MediaID,
		PersonVersion: newVersion,
		Membership:    treeAccess.Membership,
	}, nil
}

func (s *Service) resultWithDownload(
	ctx context.Context,
	asset domain.MediaAsset,
	membership treedomain.TreeMember,
) (Result, error) {
	variants, err := s.repository.ListVariantsAccessible(
		ctx,
		asset.TreeID,
		membership.UserID,
		[]uuid.UUID{asset.ID},
	)
	if err != nil {
		return Result{}, err
	}
	return s.resultWithKnownVariants(ctx, asset, membership, variants[asset.ID])
}

func (s *Service) resultWithKnownVariants(
	ctx context.Context,
	asset domain.MediaAsset,
	membership treedomain.TreeMember,
	variants []domain.MediaVariant,
) (Result, error) {
	result := Result{Asset: asset, Membership: membership}
	if !domain.CanDownload(asset.Status) {
		return result, nil
	}
	download, err := s.objectStore.PresignDownload(ctx, asset.ObjectKey, asset.OriginalFilename)
	if err != nil {
		return Result{}, err
	}
	result.Download = &download
	result.Variants = make([]VariantResult, 0, len(variants))
	for _, variant := range variants {
		variantDownload, err := s.objectStore.PresignView(ctx, variant.ObjectKey)
		if err != nil {
			return Result{}, err
		}
		result.Variants = append(result.Variants, VariantResult{
			Variant:  variant,
			Download: variantDownload,
		})
	}
	return result, nil
}

func (s *Service) readableTree(
	ctx context.Context,
	actorUserID uuid.UUID,
	treeID uuid.UUID,
) (treedomain.TreeAccess, error) {
	if actorUserID == uuid.Nil || treeID == uuid.Nil {
		return treedomain.TreeAccess{}, domain.ErrMediaNotFound
	}
	access, err := s.treeRepository.GetAccessible(ctx, treeID, actorUserID, false)
	if errors.Is(err, treedomain.ErrTreeNotFound) {
		return treedomain.TreeAccess{}, domain.ErrMediaNotFound
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
		return treedomain.TreeAccess{}, domain.ErrMediaAccessDenied
	}
	return access, nil
}

func listFilter(command ListCommand) (ListFilter, int, error) {
	limit := command.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	if limit < 1 || limit > maxListLimit ||
		(command.Kind != "" && !domain.IsKind(command.Kind)) ||
		(command.Status != "" && !isListStatus(command.Status)) {
		return ListFilter{}, 0, domain.ErrInvalidMedia
	}
	cursor, err := decodeCursor(command.Cursor)
	if err != nil {
		return ListFilter{}, 0, domain.ErrInvalidMedia
	}
	return ListFilter{
		ActorUserID:   command.ActorUserID,
		TreeID:        command.TreeID,
		Kind:          strings.TrimSpace(command.Kind),
		Status:        strings.TrimSpace(command.Status),
		BeforeCreated: cursor.CreatedAt,
		BeforeMediaID: cursor.MediaID,
		Limit:         limit + 1,
	}, limit, nil
}

func isListStatus(value string) bool {
	switch value {
	case domain.StatusPending, domain.StatusUploaded, domain.StatusProcessing,
		domain.StatusReady, domain.StatusRejected:
		return true
	default:
		return false
	}
}

func encodeCursor(cursor listCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode media cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeCursor(value string) (listCursor, error) {
	if strings.TrimSpace(value) == "" {
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
	if cursor.CreatedAt.IsZero() || cursor.MediaID == uuid.Nil {
		return listCursor{}, domain.ErrInvalidMedia
	}
	return cursor, nil
}
