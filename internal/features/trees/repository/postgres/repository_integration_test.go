package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	treepostgres "github.com/ZheglY/family_tree_app/internal/features/trees/repository/postgres"
	treeservice "github.com/ZheglY/family_tree_app/internal/features/trees/service"
	"github.com/ZheglY/family_tree_app/internal/testdatabase"
	"github.com/ZheglY/family_tree_app/migrations"
	"github.com/google/uuid"
)

func TestRepositoryEnforcesTenantIsolationAndTreeLifecycle(t *testing.T) {
	databaseURL := os.Getenv("FAMILY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("FAMILY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
		t.Fatalf("create migration runner: %v", err)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("apply migrations twice: %v", err)
	}
	if version, err := runner.CurrentVersion(ctx); err != nil || version != 9 {
		t.Fatalf("migration version = %d, error = %v", version, err)
	}

	repository := treepostgres.New(database.Pool)
	ownerID := uuid.New()
	outsiderID := uuid.New()
	treeID := uuid.New()
	createdAt := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	created, err := domain.NewFamilyTree(
		treeID,
		ownerID,
		domain.CreateValues{
			Name:        "Род Волконских",
			Description: "Семейный архив",
			Locale:      "ru-RU",
			Timezone:    "Europe/Moscow",
		},
		createdAt,
	)
	if err != nil {
		t.Fatalf("create domain tree: %v", err)
	}
	if err := repository.CreateWithOwner(ctx, created); err != nil {
		t.Fatalf("CreateWithOwner() error = %v", err)
	}

	var treeCount, memberCount int
	if err := database.Pool.QueryRow(ctx, "SELECT count(*) FROM family_trees").Scan(&treeCount); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(ctx, "SELECT count(*) FROM tree_members").Scan(&memberCount); err != nil {
		t.Fatal(err)
	}
	if treeCount != 1 || memberCount != 1 {
		t.Fatalf("atomic create counts = trees %d, members %d", treeCount, memberCount)
	}

	ownerTrees, err := repository.ListAccessible(ctx, ownerID)
	if err != nil || len(ownerTrees) != 1 {
		t.Fatalf("owner list length = %d, error = %v", len(ownerTrees), err)
	}
	outsiderTrees, err := repository.ListAccessible(ctx, outsiderID)
	if err != nil || len(outsiderTrees) != 0 {
		t.Fatalf("outsider list length = %d, error = %v", len(outsiderTrees), err)
	}
	if _, err := repository.GetAccessible(ctx, treeID, outsiderID, false); !errors.Is(err, domain.ErrTreeNotFound) {
		t.Fatalf("outsider GetAccessible() error = %v, want ErrTreeNotFound", err)
	}

	service := treeservice.New(repository)
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{
			name: "read",
			run: func() error {
				_, err := service.Get(ctx, outsiderID, treeID)
				return err
			},
		},
		{
			name: "update",
			run: func() error {
				name := "Compromised"
				_, err := service.Update(ctx, treeservice.UpdateCommand{
					ActorUserID: outsiderID,
					TreeID:      treeID,
					Version:     1,
					Name:        &name,
				})
				return err
			},
		},
		{
			name: "delete",
			run: func() error {
				return service.Delete(ctx, treeservice.MutationCommand{
					ActorUserID: outsiderID,
					TreeID:      treeID,
					Version:     1,
				})
			},
		},
	} {
		t.Run("outsider_cannot_"+operation.name, func(t *testing.T) {
			if err := operation.run(); !errors.Is(err, domain.ErrTreeNotFound) {
				t.Fatalf("error = %v, want ErrTreeNotFound", err)
			}
		})
	}

	newName := "Обновлённый род Волконских"
	updated, err := service.Update(ctx, treeservice.UpdateCommand{
		ActorUserID: ownerID,
		TreeID:      treeID,
		Version:     1,
		Name:        &newName,
	})
	if err != nil {
		t.Fatalf("owner Update() error = %v", err)
	}
	if updated.Tree.Version != 2 || updated.Tree.Name != newName {
		t.Fatalf("updated tree = %#v", updated.Tree)
	}
	if _, err := service.Update(ctx, treeservice.UpdateCommand{
		ActorUserID: ownerID,
		TreeID:      treeID,
		Version:     1,
		Name:        &newName,
	}); !errors.Is(err, domain.ErrTreeVersionConflict) {
		t.Fatalf("stale Update() error = %v", err)
	}

	if err := service.Delete(ctx, treeservice.MutationCommand{
		ActorUserID: ownerID,
		TreeID:      treeID,
		Version:     2,
		RequestID:   "request-delete",
		IPAddress:   "127.0.0.1",
	}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := service.Get(ctx, ownerID, treeID); !errors.Is(err, domain.ErrTreeNotFound) {
		t.Fatalf("Get() after delete error = %v", err)
	}
	deleted, err := repository.GetAccessible(ctx, treeID, ownerID, true)
	if err != nil || deleted.Tree.DeletedAt == nil || deleted.Tree.Version != 3 {
		t.Fatalf("deleted tree = %#v, error = %v", deleted.Tree, err)
	}
	assertAuditEntry(t, ctx, database, treeID, "tree.deleted", "request-delete")

	restored, err := service.Restore(ctx, treeservice.MutationCommand{
		ActorUserID: ownerID,
		TreeID:      treeID,
		Version:     3,
		RequestID:   "request-restore",
		IPAddress:   "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if restored.Tree.DeletedAt != nil || restored.Tree.Version != 4 {
		t.Fatalf("restored tree = %#v", restored.Tree)
	}
	assertAuditEntry(t, ctx, database, treeID, "tree.restored", "request-restore")
}

func assertAuditEntry(
	t *testing.T,
	ctx context.Context,
	database *testdatabase.Database,
	treeID uuid.UUID,
	action string,
	requestID string,
) {
	t.Helper()
	var count int
	err := database.Pool.QueryRow(ctx, `
		SELECT count(*)
		FROM audit_log
		WHERE tree_id = $1
		  AND action = $2
		  AND request_id = $3
		  AND ip_address = '127.0.0.1'::inet
	`, treeID, action, requestID).Scan(&count)
	if err != nil {
		t.Fatalf("query audit entry: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit entry %q count = %d, want 1", action, count)
	}
}
