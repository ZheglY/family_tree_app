package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/storage"
	"github.com/ZheglY/family_tree_app/internal/features/media/domain"
	treedomain "github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	"github.com/google/uuid"
)

type mediaRepositoryStub struct {
	createCalled bool
	listCalled   bool
}

func (r *mediaRepositoryStub) CreateIntentEditable(context.Context, uuid.UUID, domain.MediaAsset) (domain.MediaAsset, bool, error) {
	r.createCalled = true
	return domain.MediaAsset{}, false, nil
}

func (r *mediaRepositoryStub) GetAccessible(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (domain.MediaAsset, error) {
	return domain.MediaAsset{}, domain.ErrMediaNotFound
}

func (r *mediaRepositoryStub) ListAccessible(context.Context, ListFilter) ([]domain.MediaAsset, error) {
	r.listCalled = true
	return nil, nil
}

func (r *mediaRepositoryStub) ListVariantsAccessible(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	[]uuid.UUID,
) (map[uuid.UUID][]domain.MediaVariant, error) {
	return map[uuid.UUID][]domain.MediaVariant{}, nil
}

func (r *mediaRepositoryStub) CompleteUploadEditable(context.Context, uuid.UUID, domain.MediaAsset) error {
	return nil
}

func (r *mediaRepositoryStub) UpdateEditable(context.Context, uuid.UUID, domain.MediaAsset) error {
	return nil
}

func (r *mediaRepositoryStub) SoftDeleteEditable(context.Context, AuditMutation) error {
	return nil
}

func (r *mediaRepositoryStub) AttachToPersonEditable(context.Context, uuid.UUID, domain.PersonMedia) error {
	return nil
}

func (r *mediaRepositoryStub) DetachFromPersonEditable(context.Context, AttachmentMutation) error {
	return nil
}

func (r *mediaRepositoryStub) SetPrimaryPersonMediaEditable(context.Context, PrimaryMediaMutation) (int, error) {
	return 0, nil
}

type mediaTreeRepositoryStub struct {
	access treedomain.TreeAccess
	err    error
}

func (r mediaTreeRepositoryStub) GetAccessible(context.Context, uuid.UUID, uuid.UUID, bool) (treedomain.TreeAccess, error) {
	return r.access, r.err
}

type mediaObjectStoreStub struct{}

func (mediaObjectStoreStub) PresignUpload(context.Context, storage.UploadInput) (storage.PresignedRequest, error) {
	return storage.PresignedRequest{}, nil
}

func (mediaObjectStoreStub) HeadObject(context.Context, string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, nil
}

func (mediaObjectStoreStub) PresignDownload(context.Context, string, string) (storage.PresignedRequest, error) {
	return storage.PresignedRequest{}, nil
}

func (mediaObjectStoreStub) PresignView(context.Context, string) (storage.PresignedRequest, error) {
	return storage.PresignedRequest{}, nil
}

func TestCreateUploadIntentRejectsViewerBeforeRepositoryMutation(t *testing.T) {
	t.Parallel()
	actorID := uuid.New()
	repository := &mediaRepositoryStub{}
	service := New(
		repository,
		mediaTreeRepositoryStub{access: mediaTestTree(t, actorID, treedomain.RoleViewer)},
		mediaObjectStoreStub{},
		1024,
	)
	_, err := service.CreateUploadIntent(context.Background(), UploadIntentCommand{
		ActorUserID: actorID,
		TreeID:      uuid.New(),
		Values: domain.CreateValues{
			ClientRequestID: uuid.New(), Kind: domain.KindPhoto,
			OriginalFilename: "photo.jpg", MIMEType: "image/jpeg",
			SizeBytes: 10, ChecksumSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	})
	if !errors.Is(err, domain.ErrMediaAccessDenied) {
		t.Fatalf("CreateUploadIntent() error = %v, want ErrMediaAccessDenied", err)
	}
	if repository.createCalled {
		t.Fatal("repository mutation was called for viewer")
	}
}

func TestListRejectsInvalidCursorBeforeRepositoryQuery(t *testing.T) {
	t.Parallel()
	actorID := uuid.New()
	repository := &mediaRepositoryStub{}
	service := New(
		repository,
		mediaTreeRepositoryStub{access: mediaTestTree(t, actorID, treedomain.RoleOwner)},
		mediaObjectStoreStub{},
		1024,
	)
	_, err := service.List(context.Background(), ListCommand{
		ActorUserID: actorID,
		TreeID:      uuid.New(),
		Cursor:      "not-a-cursor",
	})
	if !errors.Is(err, domain.ErrInvalidMedia) {
		t.Fatalf("List() error = %v, want ErrInvalidMedia", err)
	}
	if repository.listCalled {
		t.Fatal("repository list was called for invalid cursor")
	}
}

func mediaTestTree(t *testing.T, userID uuid.UUID, role string) treedomain.TreeAccess {
	t.Helper()
	access, err := treedomain.NewFamilyTree(
		uuid.New(),
		userID,
		treedomain.CreateValues{Name: "Tree", Locale: "ru-RU", Timezone: "UTC"},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	access.Membership.Role = role
	return access
}
