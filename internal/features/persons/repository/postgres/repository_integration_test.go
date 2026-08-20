package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	persondomain "github.com/ZheglY/family_tree_app/internal/features/persons/domain"
	personpostgres "github.com/ZheglY/family_tree_app/internal/features/persons/repository/postgres"
	personservice "github.com/ZheglY/family_tree_app/internal/features/persons/service"
	treedomain "github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	treepostgres "github.com/ZheglY/family_tree_app/internal/features/trees/repository/postgres"
	"github.com/ZheglY/family_tree_app/internal/testdatabase"
	"github.com/ZheglY/family_tree_app/migrations"
	"github.com/google/uuid"
)

func TestPersonRepositoryLifecycleSearchAndTenantIsolation(t *testing.T) {
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
		t.Fatal(err)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	treeRepository := treepostgres.New(database.Pool)
	personRepository := personpostgres.New(database.Pool)
	service := personservice.New(personRepository, treeRepository)
	ownerA := uuid.New()
	ownerB := uuid.New()
	outsider := uuid.New()
	treeA := createTree(t, ctx, treeRepository, ownerA, "Tree A")
	treeB := createTree(t, ctx, treeRepository, ownerB, "Tree B")

	anna := createPerson(t, ctx, service, ownerA, treeA, persondomain.CreateValues{
		Sex:        persondomain.SexFemale,
		LifeStatus: persondomain.LifeStatusAlive,
		Biography:  "История Анны",
		Name: persondomain.NameValues{
			GivenName: "Анна", FamilyName: "Волконская", LanguageCode: "ru",
		},
	})
	createPerson(t, ctx, service, ownerA, treeA, persondomain.CreateValues{
		Sex: persondomain.SexMale, LifeStatus: persondomain.LifeStatusDeceased,
		Name: persondomain.NameValues{GivenName: "Борис", FamilyName: "Волконский"},
	})
	createPerson(t, ctx, service, ownerA, treeA, persondomain.CreateValues{
		Sex: persondomain.SexFemale, LifeStatus: persondomain.LifeStatusUnknown,
		Name: persondomain.NameValues{GivenName: "Вера", FamilyName: "Волконская"},
	})

	var personCount, nameCount int
	if err := database.Pool.QueryRow(ctx, "SELECT count(*) FROM persons WHERE tree_id = $1", treeA).Scan(&personCount); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(ctx, "SELECT count(*) FROM person_names WHERE tree_id = $1", treeA).Scan(&nameCount); err != nil {
		t.Fatal(err)
	}
	if personCount != 3 || nameCount != 3 {
		t.Fatalf("atomic aggregate counts = persons %d, names %d", personCount, nameCount)
	}

	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{
			name: "read",
			run: func() error {
				_, err := service.Get(ctx, outsider, treeA, anna.Card.Person.ID)
				return err
			},
		},
		{
			name: "list",
			run: func() error {
				_, err := service.List(ctx, personservice.ListCommand{ActorUserID: outsider, TreeID: treeA})
				return err
			},
		},
		{
			name: "create",
			run: func() error {
				_, err := service.Create(ctx, personservice.CreateCommand{
					ActorUserID: ownerB,
					TreeID:      treeA,
					Values: persondomain.CreateValues{
						Name: persondomain.NameValues{GivenName: "Чужой"},
					},
				})
				return err
			},
		},
	} {
		t.Run("outsider_cannot_"+operation.name, func(t *testing.T) {
			if err := operation.run(); !errors.Is(err, persondomain.ErrPersonNotFound) {
				t.Fatalf("error = %v, want ErrPersonNotFound", err)
			}
		})
	}

	acceptedAt := time.Now().UTC()
	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO tree_members (
			tree_id, user_id, role, status, created_at, accepted_at
		) VALUES ($1, $2, 'viewer', 'active', $3, $3)
	`, treeA, ownerB, acceptedAt); err != nil {
		t.Fatalf("insert viewer membership: %v", err)
	}
	if _, err := service.Get(ctx, ownerB, treeA, anna.Card.Person.ID); err != nil {
		t.Fatalf("viewer Get() error = %v", err)
	}
	biography := "Viewer mutation"
	if _, err := service.Update(ctx, personservice.UpdateCommand{
		ActorUserID: ownerB,
		TreeID:      treeA,
		PersonID:    anna.Card.Person.ID,
		Version:     1,
		Values:      persondomain.UpdateValues{Biography: &biography},
	}); !errors.Is(err, persondomain.ErrPersonAccessDenied) {
		t.Fatalf("viewer Update() error = %v", err)
	}

	pageOne, err := service.List(ctx, personservice.ListCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		Limit:       2,
	})
	if err != nil || len(pageOne.Items) != 2 || pageOne.NextCursor == "" {
		t.Fatalf("page one = %#v, error = %v", pageOne, err)
	}
	pageTwo, err := service.List(ctx, personservice.ListCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		Limit:       2,
		Cursor:      pageOne.NextCursor,
	})
	if err != nil || len(pageTwo.Items) != 1 || pageTwo.NextCursor != "" {
		t.Fatalf("page two = %#v, error = %v", pageTwo, err)
	}
	if pageOne.Items[0].Person.ID == pageTwo.Items[0].Person.ID ||
		pageOne.Items[1].Person.ID == pageTwo.Items[0].Person.ID {
		t.Fatal("cursor pagination returned a duplicate person")
	}
	search, err := service.List(ctx, personservice.ListCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		Query:       "анна",
	})
	if err != nil || len(search.Items) != 1 || search.Items[0].Person.ID != anna.Card.Person.ID {
		t.Fatalf("search result = %#v, error = %v", search, err)
	}

	updatedBiography := "Обновлённая история Анны"
	updatedName := persondomain.NameValues{
		GivenName: "Анна", Patronymic: "Петровна", FamilyName: "Болконская", LanguageCode: "ru",
	}
	updated, err := service.Update(ctx, personservice.UpdateCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		PersonID:    anna.Card.Person.ID,
		Version:     1,
		Values: persondomain.UpdateValues{
			Biography:     &updatedBiography,
			PreferredName: &updatedName,
		},
	})
	if err != nil {
		t.Fatalf("owner Update() error = %v", err)
	}
	if updated.Card.Person.Version != 2 ||
		updated.Card.PreferredName.FullText != "Анна Петровна Болконская" {
		t.Fatalf("updated card = %#v", updated.Card)
	}
	if _, err := service.Update(ctx, personservice.UpdateCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		PersonID:    anna.Card.Person.ID,
		Version:     1,
		Values:      persondomain.UpdateValues{Biography: &updatedBiography},
	}); !errors.Is(err, persondomain.ErrPersonVersionConflict) {
		t.Fatalf("stale Update() error = %v", err)
	}

	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO person_names (
			id, person_id, tree_id, type, full_text, is_preferred,
			language_code, created_at, updated_at
		) VALUES ($1, $2, $3, 'other', 'Cross tenant', false, 'en', now(), now())
	`, uuid.New(), anna.Card.Person.ID, treeB); err == nil {
		t.Fatal("cross-tree person name insert succeeded")
	}

	if err := service.Delete(ctx, personservice.MutationCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		PersonID:    anna.Card.Person.ID,
		Version:     2,
		RequestID:   "person-delete",
		IPAddress:   "127.0.0.1",
	}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := service.Get(ctx, ownerA, treeA, anna.Card.Person.ID); !errors.Is(err, persondomain.ErrPersonNotFound) {
		t.Fatalf("Get() after delete error = %v", err)
	}
	restored, err := service.Restore(ctx, personservice.MutationCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		PersonID:    anna.Card.Person.ID,
		Version:     3,
		RequestID:   "person-restore",
		IPAddress:   "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if restored.Card.Person.DeletedAt != nil || restored.Card.Person.Version != 4 {
		t.Fatalf("restored person = %#v", restored.Card.Person)
	}
	var auditCount int
	if err := database.Pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log
		WHERE tree_id = $1 AND entity_id = $2
		  AND action IN ('person.deleted', 'person.restored')
	`, treeA, anna.Card.Person.ID).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("person audit count = %d, error = %v", auditCount, err)
	}
}

func createTree(
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

func createPerson(
	t *testing.T,
	ctx context.Context,
	service *personservice.Service,
	ownerID uuid.UUID,
	treeID uuid.UUID,
	values persondomain.CreateValues,
) personservice.Result {
	t.Helper()
	created, err := service.Create(ctx, personservice.CreateCommand{
		ActorUserID: ownerID,
		TreeID:      treeID,
		Values:      values,
	})
	if err != nil {
		t.Fatalf("create person: %v", err)
	}
	return created
}
