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
	"github.com/ZheglY/family_tree_app/internal/features/exports/domain"
	exportpostgres "github.com/ZheglY/family_tree_app/internal/features/exports/repository/postgres"
	exportservice "github.com/ZheglY/family_tree_app/internal/features/exports/service"
	treedomain "github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	treepostgres "github.com/ZheglY/family_tree_app/internal/features/trees/repository/postgres"
	"github.com/ZheglY/family_tree_app/internal/testdatabase"
	"github.com/ZheglY/family_tree_app/migrations"
	"github.com/google/uuid"
)

type exportObjectStoreStub struct{}

func (exportObjectStoreStub) PresignUpload(context.Context, storage.UploadInput) (storage.PresignedRequest, error) {
	return storage.PresignedRequest{}, errors.New("not implemented")
}

func (exportObjectStoreStub) HeadObject(context.Context, string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, errors.New("not implemented")
}

func (exportObjectStoreStub) PresignDownload(
	_ context.Context, objectKey string, _ string,
) (storage.PresignedRequest, error) {
	return storage.PresignedRequest{
		URL: "https://download.example/" + objectKey, Method: http.MethodGet,
		Headers: make(http.Header), ExpiresAt: time.Now().UTC().Add(time.Minute),
	}, nil
}

func (exportObjectStoreStub) PresignView(context.Context, string) (storage.PresignedRequest, error) {
	return storage.PresignedRequest{}, errors.New("not implemented")
}

func TestExportRepositoryLifecycleSnapshotAndTenantIsolation(t *testing.T) {
	databaseURL := os.Getenv("FAMILY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("FAMILY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	database, err := testdatabase.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}

	ownerID, editorID, viewerID, outsiderID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	treeRepository := treepostgres.New(database.Pool)
	treeID := createExportTree(t, ctx, treeRepository, ownerID)
	acceptedAt := time.Now().UTC()
	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO tree_members (tree_id, user_id, role, status, created_at, accepted_at)
		VALUES ($1, $2, 'editor', 'active', $4, $4),
		       ($1, $3, 'viewer', 'active', $4, $4)
	`, treeID, editorID, viewerID, acceptedAt); err != nil {
		t.Fatalf("insert export collaborators: %v", err)
	}
	personID, nameID := uuid.New(), uuid.New()
	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO persons (
			id, tree_id, sex, life_status, biography, notes, privacy_level,
			created_by, updated_by, created_at, updated_at
		) VALUES ($1, $2, 'female', 'deceased', 'Архивная биография', '', 'tree_members', $3, $3, $4, $4);
	`, personID, treeID, editorID, acceptedAt); err != nil {
		t.Fatalf("insert export person: %v", err)
	}
	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO person_names (
			id, person_id, tree_id, type, given_name, full_text, is_preferred,
			language_code, created_at, updated_at
		) VALUES ($1, $2, $3, 'primary', 'Анна', 'Анна Волконская', true, 'ru-RU', $4, $4)
	`, nameID, personID, treeID, acceptedAt); err != nil {
		t.Fatalf("insert export person name: %v", err)
	}

	repository := exportpostgres.New(database.Pool)
	service := exportservice.New(repository, treeRepository, exportObjectStoreStub{})
	clientRequestID := uuid.New()
	command := exportservice.CreateCommand{
		ActorUserID: editorID, TreeID: treeID,
		Values:    domain.CreateValues{ClientRequestID: clientRequestID, Format: domain.FormatJSONBackup},
		RequestID: "request-export-create", IPAddress: "127.0.0.1",
	}
	created, err := service.Create(ctx, command)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !created.Created || created.Export.Status != domain.StatusQueued {
		t.Fatalf("created export = %#v", created)
	}
	retried, err := service.Create(ctx, command)
	if err != nil || retried.Created || retried.Export.ID != created.Export.ID {
		t.Fatalf("idempotent export = %#v, error = %v", retried, err)
	}
	if _, err := service.Create(ctx, exportservice.CreateCommand{
		ActorUserID: viewerID, TreeID: treeID,
		Values: domain.CreateValues{ClientRequestID: uuid.New(), Format: domain.FormatJSONBackup},
	}); !errors.Is(err, domain.ErrExportAccessDenied) {
		t.Fatalf("viewer Create() error = %v", err)
	}
	if _, err := service.Get(ctx, outsiderID, treeID, created.Export.ID); !errors.Is(err, domain.ErrExportNotFound) {
		t.Fatalf("outsider Get() error = %v", err)
	}
	if _, err := service.Get(ctx, viewerID, treeID, created.Export.ID); !errors.Is(err, domain.ErrExportNotFound) {
		t.Fatalf("non-requesting viewer Get() error = %v", err)
	}
	if _, err := service.Get(ctx, ownerID, treeID, created.Export.ID); err != nil {
		t.Fatalf("owner Get() error = %v", err)
	}

	var generateJobs, createAudits int
	if err := database.Pool.QueryRow(ctx, `
		SELECT count(*) FROM background_jobs
		WHERE kind = 'export.generate' AND deduplication_key = $1
	`, created.Export.ID.String()).Scan(&generateJobs); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log
		WHERE tree_id = $1 AND action = 'export.created' AND request_id = 'request-export-create'
	`, treeID).Scan(&createAudits); err != nil {
		t.Fatal(err)
	}
	if generateJobs != 1 || createAudits != 1 {
		t.Fatalf("generate jobs = %d, create audits = %d", generateJobs, createAudits)
	}

	running, err := repository.AcquireForGeneration(ctx, treeID, created.Export.ID, time.Now().UTC())
	if err != nil || running.Status != domain.StatusRunning {
		t.Fatalf("AcquireForGeneration() = %#v, error = %v", running, err)
	}
	snapshot, err := repository.LoadManifest(ctx, running)
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	if snapshot.Schema.Name != domain.ManifestSchemaName || snapshot.Tree.ID != treeID ||
		len(snapshot.Members) != 3 || len(snapshot.Persons) != 1 ||
		len(snapshot.PersonNames) != 1 || snapshot.Persons[0].Biography != "Архивная биография" {
		t.Fatalf("manifest snapshot = %#v", snapshot)
	}
	now := time.Now().UTC()
	expiresAt := now.Add(time.Hour)
	checksum := strings.Repeat("a", 64)
	objectKey := "trees/" + treeID.String() + "/exports/" + created.Export.ID.String() + "/manifest.json"
	if err := repository.MarkCompleted(
		ctx, running, objectKey, "application/json", 128, checksum, expiresAt, now,
	); err != nil {
		t.Fatalf("MarkCompleted() error = %v", err)
	}
	if err := repository.MarkCompleted(
		ctx, running, objectKey+".racing", "application/json", 129,
		strings.Repeat("b", 64), expiresAt, now,
	); !errors.Is(err, domain.ErrExportStateConflict) {
		t.Fatalf("racing MarkCompleted() error = %v, want ErrExportStateConflict", err)
	}
	download, err := service.Download(ctx, exportservice.MutationCommand{
		ActorUserID: ownerID, TreeID: treeID, ExportID: created.Export.ID,
		RequestID: "request-export-download", IPAddress: "127.0.0.1",
	})
	if err != nil || download.Download == nil || !strings.Contains(download.Download.URL, objectKey) {
		t.Fatalf("Download() = %#v, error = %v", download, err)
	}
	listed, err := service.List(ctx, exportservice.ListCommand{ActorUserID: editorID, TreeID: treeID, Limit: 10})
	if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != created.Export.ID {
		t.Fatalf("List() = %#v, error = %v", listed, err)
	}
	if err := service.Delete(ctx, exportservice.MutationCommand{
		ActorUserID: ownerID, TreeID: treeID, ExportID: created.Export.ID,
		RequestID: "request-export-delete", IPAddress: "127.0.0.1",
	}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	deleted, err := service.Get(ctx, ownerID, treeID, created.Export.ID)
	if err != nil || deleted.Export.Status != domain.StatusExpired {
		t.Fatalf("deleted export = %#v, error = %v", deleted, err)
	}
	if _, err := service.Download(ctx, exportservice.MutationCommand{
		ActorUserID: ownerID, TreeID: treeID, ExportID: created.Export.ID,
	}); !errors.Is(err, domain.ErrExportResultUnavailable) {
		t.Fatalf("Download() after delete error = %v", err)
	}
	var deleteJobs, downloadAudits, deleteAudits int
	if err := database.Pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM background_jobs WHERE kind = 'export.delete' AND deduplication_key = $1),
			(SELECT count(*) FROM audit_log WHERE tree_id = $2 AND action = 'export.downloaded'),
			(SELECT count(*) FROM audit_log WHERE tree_id = $2 AND action = 'export.deleted')
	`, created.Export.ID.String(), treeID).Scan(&deleteJobs, &downloadAudits, &deleteAudits); err != nil {
		t.Fatal(err)
	}
	if deleteJobs != 1 || downloadAudits != 1 || deleteAudits != 1 {
		t.Fatalf("delete jobs = %d, download audits = %d, delete audits = %d", deleteJobs, downloadAudits, deleteAudits)
	}
}

func createExportTree(
	t *testing.T,
	ctx context.Context,
	repository *treepostgres.Repository,
	ownerID uuid.UUID,
) uuid.UUID {
	t.Helper()
	treeID := uuid.New()
	created, err := treedomain.NewFamilyTree(treeID, ownerID, treedomain.CreateValues{
		Name: "Род Волконских", Locale: "ru-RU", Timezone: "Europe/Moscow",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateWithOwner(ctx, created); err != nil {
		t.Fatal(err)
	}
	return treeID
}

var _ storage.ObjectStore = exportObjectStoreStub{}
