package postgres_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/core/storage"
	"github.com/ZheglY/family_tree_app/internal/features/media/domain"
	mediapostgres "github.com/ZheglY/family_tree_app/internal/features/media/repository/postgres"
	mediaservice "github.com/ZheglY/family_tree_app/internal/features/media/service"
	persondomain "github.com/ZheglY/family_tree_app/internal/features/persons/domain"
	personpostgres "github.com/ZheglY/family_tree_app/internal/features/persons/repository/postgres"
	personservice "github.com/ZheglY/family_tree_app/internal/features/persons/service"
	treedomain "github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	treepostgres "github.com/ZheglY/family_tree_app/internal/features/trees/repository/postgres"
	"github.com/ZheglY/family_tree_app/internal/testdatabase"
	"github.com/ZheglY/family_tree_app/migrations"
	"github.com/google/uuid"
)

type mediaObjectStoreStub struct {
	objects map[string]storage.ObjectInfo
}

func (s *mediaObjectStoreStub) PresignUpload(
	_ context.Context,
	input storage.UploadInput,
) (storage.PresignedRequest, error) {
	return storage.PresignedRequest{
		URL:       "https://upload.example/" + input.ObjectKey,
		Method:    http.MethodPut,
		Headers:   http.Header{"Content-Type": []string{input.ContentType}},
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}, nil
}

func (s *mediaObjectStoreStub) HeadObject(
	_ context.Context,
	objectKey string,
) (storage.ObjectInfo, error) {
	object, exists := s.objects[objectKey]
	if !exists {
		return storage.ObjectInfo{}, storage.ErrObjectNotFound
	}
	return object, nil
}

func (s *mediaObjectStoreStub) PresignDownload(
	_ context.Context,
	objectKey string,
	_ string,
) (storage.PresignedRequest, error) {
	return storage.PresignedRequest{
		URL:       "https://download.example/" + objectKey,
		Method:    http.MethodGet,
		Headers:   make(http.Header),
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}, nil
}

func TestMediaRepositoryLifecycleIdempotencyAndTenantIsolation(t *testing.T) {
	databaseURL := os.Getenv("FAMILY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("FAMILY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	database, err := testdatabase.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	}()
	runner, err := migrations.NewRunner(database.Pool, logger.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	treeRepository := treepostgres.New(database.Pool)
	personRepository := personpostgres.New(database.Pool)
	personService := personservice.New(personRepository, treeRepository)
	mediaRepository := mediapostgres.New(database.Pool)
	objectStore := &mediaObjectStoreStub{objects: make(map[string]storage.ObjectInfo)}
	mediaService := mediaservice.New(mediaRepository, treeRepository, objectStore, 1024*1024)
	ownerA := uuid.New()
	ownerB := uuid.New()
	viewer := uuid.New()
	outsider := uuid.New()
	treeA := mediaCreateTree(t, ctx, treeRepository, ownerA, "Tree A")
	treeB := mediaCreateTree(t, ctx, treeRepository, ownerB, "Tree B")
	personA := mediaCreatePerson(t, ctx, personService, ownerA, treeA, "Анна")
	personB := mediaCreatePerson(t, ctx, personService, ownerB, treeB, "Чужая персона")

	checksum := strings.Repeat("a", 64)
	requestID := uuid.New()
	intentCommand := mediaservice.UploadIntentCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		Values: domain.CreateValues{
			ClientRequestID:  requestID,
			Kind:             domain.KindPhoto,
			OriginalFilename: "portrait.jpg",
			MIMEType:         "image/jpeg",
			SizeBytes:        1024,
			ChecksumSHA256:   checksum,
		},
	}
	created, err := mediaService.CreateUploadIntent(ctx, intentCommand)
	if err != nil {
		t.Fatalf("CreateUploadIntent() error = %v", err)
	}
	if !created.Created || created.Upload == nil || created.Asset.Status != domain.StatusPending ||
		created.Asset.Version != 1 {
		t.Fatalf("created intent = %#v", created)
	}
	retried, err := mediaService.CreateUploadIntent(ctx, intentCommand)
	if err != nil {
		t.Fatalf("idempotent CreateUploadIntent() error = %v", err)
	}
	if retried.Created || retried.Asset.ID != created.Asset.ID || retried.Upload == nil {
		t.Fatalf("retried intent = %#v", retried)
	}
	conflictingCommand := intentCommand
	conflictingCommand.Values.OriginalFilename = "different.jpg"
	if _, err := mediaService.CreateUploadIntent(ctx, conflictingCommand); !errors.Is(err, domain.ErrMediaRequestConflict) {
		t.Fatalf("conflicting intent error = %v", err)
	}
	if _, err := mediaService.CompleteUpload(ctx, ownerA, treeA, created.Asset.ID); !errors.Is(err, domain.ErrUploadedObjectNotFound) {
		t.Fatalf("missing object completion error = %v", err)
	}

	acceptedAt := time.Now().UTC()
	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO tree_members (
			tree_id, user_id, role, status, created_at, accepted_at
		) VALUES ($1, $2, 'viewer', 'active', $3, $3)
	`, treeA, viewer, acceptedAt); err != nil {
		t.Fatalf("insert viewer membership: %v", err)
	}
	if _, err := mediaService.Get(ctx, viewer, treeA, created.Asset.ID); err != nil {
		t.Fatalf("viewer Get() error = %v", err)
	}
	if _, err := mediaService.CreateUploadIntent(ctx, mediaservice.UploadIntentCommand{
		ActorUserID: viewer,
		TreeID:      treeA,
		Values: domain.CreateValues{
			ClientRequestID: uuid.New(), Kind: domain.KindPhoto,
			OriginalFilename: "viewer.jpg", MIMEType: "image/jpeg",
			SizeBytes: 1, ChecksumSHA256: checksum,
		},
	}); !errors.Is(err, domain.ErrMediaAccessDenied) {
		t.Fatalf("viewer CreateUploadIntent() error = %v", err)
	}
	if _, err := mediaService.Get(ctx, outsider, treeA, created.Asset.ID); !errors.Is(err, domain.ErrMediaNotFound) {
		t.Fatalf("outsider Get() error = %v", err)
	}

	objectStore.objects[created.Asset.ObjectKey] = storage.ObjectInfo{
		ContentType:    "image/jpeg",
		SizeBytes:      1024,
		ChecksumSHA256: checksum,
		ETag:           "portrait-etag",
	}
	completed, err := mediaService.CompleteUpload(ctx, ownerA, treeA, created.Asset.ID)
	if err != nil {
		t.Fatalf("CompleteUpload() error = %v", err)
	}
	if completed.Asset.Status != domain.StatusUploaded || completed.Asset.Version != 2 ||
		completed.Download == nil {
		t.Fatalf("completed media = %#v", completed)
	}
	repeatedCompletion, err := mediaService.CompleteUpload(ctx, ownerA, treeA, created.Asset.ID)
	if err != nil || repeatedCompletion.Asset.Version != 2 {
		t.Fatalf("repeated completion = %#v, error = %v", repeatedCompletion, err)
	}

	caption := "Семейный портрет"
	description := "Оригинал хранится в семейном архиве"
	updated, err := mediaService.Update(ctx, mediaservice.UpdateCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		MediaID:     created.Asset.ID,
		Version:     2,
		Values:      domain.UpdateValues{Caption: &caption, Description: &description},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Asset.Version != 3 || updated.Asset.Caption != caption {
		t.Fatalf("updated media = %#v", updated.Asset)
	}
	if _, err := mediaService.Update(ctx, mediaservice.UpdateCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		MediaID:     created.Asset.ID,
		Version:     2,
		Values:      domain.UpdateValues{Caption: &caption},
	}); !errors.Is(err, domain.ErrMediaVersionConflict) {
		t.Fatalf("stale Update() error = %v", err)
	}

	attachment, err := mediaService.AttachToPerson(ctx, mediaservice.AttachCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		PersonID:    personA,
		MediaID:     created.Asset.ID,
		Role:        domain.RoleProfile,
		SortOrder:   1,
	})
	if err != nil {
		t.Fatalf("AttachToPerson() error = %v", err)
	}
	if attachment.Attachment.PersonID != personA || attachment.Asset.ID != created.Asset.ID {
		t.Fatalf("attachment = %#v", attachment)
	}
	if _, err := mediaService.AttachToPerson(ctx, mediaservice.AttachCommand{
		ActorUserID: ownerA, TreeID: treeA, PersonID: personA,
		MediaID: created.Asset.ID, Role: domain.RoleProfile,
	}); !errors.Is(err, domain.ErrDuplicateMediaAttachment) {
		t.Fatalf("duplicate attachment error = %v", err)
	}
	if _, err := mediaService.AttachToPerson(ctx, mediaservice.AttachCommand{
		ActorUserID: ownerA, TreeID: treeA, PersonID: personB,
		MediaID: created.Asset.ID, Role: domain.RoleOther,
	}); !errors.Is(err, domain.ErrMediaNotFound) {
		t.Fatalf("cross-tree attachment error = %v", err)
	}

	primary, err := mediaService.SetPrimaryPersonMedia(ctx, mediaservice.SetPrimaryCommand{
		ActorUserID:   ownerA,
		TreeID:        treeA,
		PersonID:      personA,
		MediaID:       created.Asset.ID,
		PersonVersion: 1,
	})
	if err != nil || primary.PersonVersion != 2 {
		t.Fatalf("SetPrimaryPersonMedia() result = %#v, error = %v", primary, err)
	}
	if _, err := mediaService.SetPrimaryPersonMedia(ctx, mediaservice.SetPrimaryCommand{
		ActorUserID: ownerA, TreeID: treeA, PersonID: personA,
		MediaID: created.Asset.ID, PersonVersion: 1,
	}); !errors.Is(err, domain.ErrPrimaryMediaConflict) {
		t.Fatalf("stale SetPrimaryPersonMedia() error = %v", err)
	}
	if err := mediaService.DetachFromPerson(ctx, mediaservice.DetachCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		PersonID:    personA,
		MediaID:     created.Asset.ID,
	}); err != nil {
		t.Fatalf("DetachFromPerson() error = %v", err)
	}
	var primaryMediaID *uuid.UUID
	var personVersion int
	if err := database.Pool.QueryRow(ctx, `
		SELECT primary_media_id, version FROM persons WHERE tree_id = $1 AND id = $2
	`, treeA, personA).Scan(&primaryMediaID, &personVersion); err != nil {
		t.Fatal(err)
	}
	if primaryMediaID != nil || personVersion != 3 {
		t.Fatalf("person after detach = primary %v, version %d", primaryMediaID, personVersion)
	}
	if err := mediaService.DetachFromPerson(ctx, mediaservice.DetachCommand{
		ActorUserID: ownerA, TreeID: treeA, PersonID: personA, MediaID: created.Asset.ID,
	}); !errors.Is(err, domain.ErrMediaAttachmentNotFound) {
		t.Fatalf("missing detachment error = %v", err)
	}

	secondIntent, err := mediaService.CreateUploadIntent(ctx, mediaservice.UploadIntentCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		Values: domain.CreateValues{
			ClientRequestID: uuid.New(), Kind: domain.KindDocument,
			OriginalFilename: "record.pdf", MIMEType: "application/pdf",
			SizeBytes: 2048, ChecksumSHA256: strings.Repeat("b", 64),
		},
	})
	if err != nil {
		t.Fatalf("create second intent: %v", err)
	}
	objectStore.objects[secondIntent.Asset.ObjectKey] = storage.ObjectInfo{
		ContentType: "application/pdf", SizeBytes: 2047,
		ChecksumSHA256: strings.Repeat("b", 64),
	}
	if _, err := mediaService.CompleteUpload(ctx, ownerA, treeA, secondIntent.Asset.ID); !errors.Is(err, domain.ErrUploadedObjectMismatch) {
		t.Fatalf("mismatched completion error = %v", err)
	}
	if _, err := mediaService.AttachToPerson(ctx, mediaservice.AttachCommand{
		ActorUserID: ownerA, TreeID: treeA, PersonID: personA,
		MediaID: secondIntent.Asset.ID, Role: domain.RoleDocument,
	}); !errors.Is(err, domain.ErrMediaStateConflict) {
		t.Fatalf("pending attachment error = %v", err)
	}

	pageOne, err := mediaService.List(ctx, mediaservice.ListCommand{
		ActorUserID: ownerA, TreeID: treeA, Limit: 1,
	})
	if err != nil || len(pageOne.Items) != 1 || pageOne.NextCursor == "" {
		t.Fatalf("page one = %#v, error = %v", pageOne, err)
	}
	pageTwo, err := mediaService.List(ctx, mediaservice.ListCommand{
		ActorUserID: ownerA, TreeID: treeA, Limit: 1, Cursor: pageOne.NextCursor,
	})
	if err != nil || len(pageTwo.Items) != 1 || pageTwo.NextCursor != "" ||
		pageTwo.Items[0].Asset.ID == pageOne.Items[0].Asset.ID {
		t.Fatalf("page two = %#v, error = %v", pageTwo, err)
	}
	uploadedOnly, err := mediaService.List(ctx, mediaservice.ListCommand{
		ActorUserID: ownerA, TreeID: treeA, Status: domain.StatusUploaded,
	})
	if err != nil || len(uploadedOnly.Items) != 1 || uploadedOnly.Items[0].Asset.ID != created.Asset.ID {
		t.Fatalf("uploaded list = %#v, error = %v", uploadedOnly, err)
	}

	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO person_media (
			tree_id, person_id, media_id, role, sort_order, created_by, created_at
		) VALUES ($1, $2, $3, 'other', 0, $4, now())
	`, treeB, personA, created.Asset.ID, ownerA); err == nil {
		t.Fatal("cross-tree person media insert succeeded")
	}
	if err := mediaService.Delete(ctx, mediaservice.MutationCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		MediaID:     created.Asset.ID,
		Version:     3,
		RequestID:   "media-delete",
		IPAddress:   "127.0.0.1",
	}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := mediaService.Get(ctx, ownerA, treeA, created.Asset.ID); !errors.Is(err, domain.ErrMediaNotFound) {
		t.Fatalf("Get() after delete error = %v", err)
	}
	var auditCount int
	if err := database.Pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log
		WHERE tree_id = $1 AND action = 'media_asset.deleted'
	`, treeA).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("media deletion audit count = %d, error = %v", auditCount, err)
	}
}

func mediaCreateTree(
	t *testing.T,
	ctx context.Context,
	repository *treepostgres.Repository,
	ownerID uuid.UUID,
	name string,
) uuid.UUID {
	t.Helper()
	treeID := uuid.New()
	access, err := treedomain.NewFamilyTree(
		treeID,
		ownerID,
		treedomain.CreateValues{Name: name, Locale: "ru-RU", Timezone: "UTC"},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateWithOwner(ctx, access); err != nil {
		t.Fatalf("create tree: %v", err)
	}
	return treeID
}

func mediaCreatePerson(
	t *testing.T,
	ctx context.Context,
	personService *personservice.Service,
	ownerID uuid.UUID,
	treeID uuid.UUID,
	name string,
) uuid.UUID {
	t.Helper()
	created, err := personService.Create(ctx, personservice.CreateCommand{
		ActorUserID: ownerID,
		TreeID:      treeID,
		Values: persondomain.CreateValues{
			Name: persondomain.NameValues{GivenName: name, LanguageCode: "ru"},
		},
	})
	if err != nil {
		t.Fatalf("create person: %v", err)
	}
	return created.Card.Person.ID
}
