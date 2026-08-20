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
	"github.com/ZheglY/family_tree_app/internal/features/relationships/domain"
	relationpostgres "github.com/ZheglY/family_tree_app/internal/features/relationships/repository/postgres"
	"github.com/ZheglY/family_tree_app/internal/features/relationships/service"
	treedomain "github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	treepostgres "github.com/ZheglY/family_tree_app/internal/features/trees/repository/postgres"
	"github.com/ZheglY/family_tree_app/internal/testdatabase"
	"github.com/ZheglY/family_tree_app/migrations"
	"github.com/google/uuid"
)

func TestRelationshipRepositoryGraphInvariantsAndTenantIsolation(t *testing.T) {
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
	relationRepository := relationpostgres.New(database.Pool)
	relationService := service.New(relationRepository, treeRepository)
	ownerA := uuid.New()
	ownerB := uuid.New()
	viewer := uuid.New()
	outsider := uuid.New()
	treeA := relationshipCreateTree(t, ctx, treeRepository, ownerA, "Tree A")
	treeB := relationshipCreateTree(t, ctx, treeRepository, ownerB, "Tree B")
	parentID := relationshipCreatePerson(t, ctx, personService, ownerA, treeA, "Анна")
	childID := relationshipCreatePerson(t, ctx, personService, ownerA, treeA, "Борис")
	grandchildID := relationshipCreatePerson(t, ctx, personService, ownerA, treeA, "Вера")
	crossTreePersonID := relationshipCreatePerson(t, ctx, personService, ownerB, treeB, "Чужая персона")

	_, err = relationService.Create(ctx, service.CreateCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		Values: domain.CreateValues{
			ParentPersonID: parentID,
			ChildPersonID:  parentID,
		},
	})
	if !errors.Is(err, domain.ErrInvalidRelation) {
		t.Fatalf("self relation error = %v, want ErrInvalidRelation", err)
	}
	_, err = relationService.Create(ctx, service.CreateCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		Values: domain.CreateValues{
			ParentPersonID: parentID,
			ChildPersonID:  crossTreePersonID,
		},
	})
	if !errors.Is(err, domain.ErrRelationNotFound) {
		t.Fatalf("cross-tree relation error = %v, want ErrRelationNotFound", err)
	}

	first := relationshipCreateRelation(
		t,
		ctx,
		relationService,
		ownerA,
		treeA,
		parentID,
		childID,
		domain.RelationBiological,
	)
	_, err = relationService.Create(ctx, service.CreateCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		Values: domain.CreateValues{
			ParentPersonID: parentID,
			ChildPersonID:  childID,
			RelationType:   domain.RelationBiological,
		},
	})
	if !errors.Is(err, domain.ErrDuplicateRelation) {
		t.Fatalf("duplicate relation error = %v, want ErrDuplicateRelation", err)
	}
	_, err = relationService.Create(ctx, service.CreateCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		Values: domain.CreateValues{
			ParentPersonID: childID,
			ChildPersonID:  parentID,
			RelationType:   domain.RelationBiological,
		},
	})
	if !errors.Is(err, domain.ErrRelationCycle) {
		t.Fatalf("direct cycle error = %v, want ErrRelationCycle", err)
	}
	second := relationshipCreateRelation(
		t,
		ctx,
		relationService,
		ownerA,
		treeA,
		childID,
		grandchildID,
		domain.RelationBiological,
	)
	_, err = relationService.Create(ctx, service.CreateCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		Values: domain.CreateValues{
			ParentPersonID: grandchildID,
			ChildPersonID:  parentID,
			RelationType:   domain.RelationBiological,
		},
	})
	if !errors.Is(err, domain.ErrRelationCycle) {
		t.Fatalf("long cycle error = %v, want ErrRelationCycle", err)
	}

	acceptedAt := time.Now().UTC()
	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO tree_members (
			tree_id, user_id, role, status, created_at, accepted_at
		) VALUES ($1, $2, 'viewer', 'active', $3, $3)
	`, treeA, viewer, acceptedAt); err != nil {
		t.Fatalf("insert viewer membership: %v", err)
	}
	if _, err := relationService.Get(ctx, viewer, treeA, first.Relation.ID); err != nil {
		t.Fatalf("viewer Get() error = %v", err)
	}
	_, err = relationService.Create(ctx, service.CreateCommand{
		ActorUserID: viewer,
		TreeID:      treeA,
		Values: domain.CreateValues{
			ParentPersonID: parentID,
			ChildPersonID:  grandchildID,
		},
	})
	if !errors.Is(err, domain.ErrRelationAccessDenied) {
		t.Fatalf("viewer Create() error = %v, want ErrRelationAccessDenied", err)
	}
	if _, err := relationService.Get(ctx, outsider, treeA, first.Relation.ID); !errors.Is(err, domain.ErrRelationNotFound) {
		t.Fatalf("outsider Get() error = %v, want ErrRelationNotFound", err)
	}
	if _, err := relationService.Graph(ctx, service.GraphCommand{
		ActorUserID:      outsider,
		TreeID:           treeA,
		CenterPersonID:   childID,
		AncestorsDepth:   1,
		DescendantsDepth: 1,
	}); !errors.Is(err, domain.ErrRelationNotFound) {
		t.Fatalf("outsider Graph() error = %v, want ErrRelationNotFound", err)
	}

	updatedType := domain.RelationAdoptive
	updatedConfidence := domain.ConfidenceConfirmed
	updated, err := relationService.Update(ctx, service.UpdateCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		RelationID:  first.Relation.ID,
		Version:     1,
		Values: domain.UpdateValues{
			RelationType: &updatedType,
			Confidence:   &updatedConfidence,
		},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Relation.Version != 2 || updated.Relation.RelationType != domain.RelationAdoptive {
		t.Fatalf("updated relation = %#v", updated.Relation)
	}
	if _, err := relationService.Update(ctx, service.UpdateCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		RelationID:  first.Relation.ID,
		Version:     1,
		Values:      domain.UpdateValues{Confidence: &updatedConfidence},
	}); !errors.Is(err, domain.ErrRelationVersionConflict) {
		t.Fatalf("stale Update() error = %v", err)
	}

	if err := relationService.Delete(ctx, service.MutationCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		RelationID:  first.Relation.ID,
		Version:     2,
		RequestID:   "delete-first-relation",
		IPAddress:   "127.0.0.1",
	}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := relationService.Get(ctx, ownerA, treeA, first.Relation.ID); !errors.Is(err, domain.ErrRelationNotFound) {
		t.Fatalf("Get() after delete error = %v", err)
	}
	reverse := relationshipCreateRelation(
		t,
		ctx,
		relationService,
		ownerA,
		treeA,
		childID,
		parentID,
		domain.RelationBiological,
	)
	if err := relationService.Delete(ctx, service.MutationCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		RelationID:  reverse.Relation.ID,
		Version:     1,
		RequestID:   "delete-reverse-relation",
		IPAddress:   "127.0.0.1",
	}); err != nil {
		t.Fatalf("delete reverse relation: %v", err)
	}
	replacement := relationshipCreateRelation(
		t,
		ctx,
		relationService,
		ownerA,
		treeA,
		parentID,
		childID,
		domain.RelationAdoptive,
	)

	graph, err := relationService.Graph(ctx, service.GraphCommand{
		ActorUserID:      viewer,
		TreeID:           treeA,
		CenterPersonID:   childID,
		AncestorsDepth:   1,
		DescendantsDepth: 1,
	})
	if err != nil {
		t.Fatalf("Graph() error = %v", err)
	}
	if len(graph.Graph.Persons) != 3 || len(graph.Graph.Relations) != 2 {
		t.Fatalf(
			"graph size = persons %d, relations %d",
			len(graph.Graph.Persons),
			len(graph.Graph.Relations),
		)
	}
	centerOnly, err := relationService.Graph(ctx, service.GraphCommand{
		ActorUserID:    ownerA,
		TreeID:         treeA,
		CenterPersonID: childID,
	})
	if err != nil || len(centerOnly.Graph.Persons) != 1 || len(centerOnly.Graph.Relations) != 0 {
		t.Fatalf("center-only graph = %#v, error = %v", centerOnly.Graph, err)
	}
	if _, err := relationRepository.LoadGraphAccessible(ctx, service.GraphFilter{
		ActorUserID:      ownerA,
		TreeID:           treeA,
		CenterPersonID:   childID,
		AncestorsDepth:   1,
		DescendantsDepth: 1,
		MaxNodes:         2,
	}); !errors.Is(err, domain.ErrGraphLimitExceeded) {
		t.Fatalf("limited graph error = %v, want ErrGraphLimitExceeded", err)
	}

	concurrentParent := relationshipCreatePerson(t, ctx, personService, ownerA, treeA, "Глеб")
	concurrentChild := relationshipCreatePerson(t, ctx, personService, ownerA, treeA, "Дарья")
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, values := range []domain.CreateValues{
		{
			ParentPersonID: concurrentParent,
			ChildPersonID:  concurrentChild,
			RelationType:   domain.RelationBiological,
		},
		{
			ParentPersonID: concurrentChild,
			ChildPersonID:  concurrentParent,
			RelationType:   domain.RelationBiological,
		},
	} {
		values := values
		go func() {
			<-start
			_, err := relationService.Create(ctx, service.CreateCommand{
				ActorUserID: ownerA,
				TreeID:      treeA,
				Values:      values,
			})
			results <- err
		}()
	}
	close(start)
	var successCount, cycleCount int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successCount++
		case errors.Is(err, domain.ErrRelationCycle):
			cycleCount++
		default:
			t.Fatalf("concurrent relation error = %v", err)
		}
	}
	if successCount != 1 || cycleCount != 1 {
		t.Fatalf("concurrent results = success %d, cycle %d", successCount, cycleCount)
	}

	var activeFirstPair, auditCount int
	if err := database.Pool.QueryRow(ctx, `
		SELECT count(*)
		FROM parent_child_relations
		WHERE tree_id = $1
		  AND parent_person_id = $2
		  AND child_person_id = $3
		  AND deleted_at IS NULL
	`, treeA, parentID, childID).Scan(&activeFirstPair); err != nil {
		t.Fatal(err)
	}
	if activeFirstPair != 1 || replacement.Relation.ID == uuid.Nil || second.Relation.ID == uuid.Nil {
		t.Fatalf("active replacement count = %d", activeFirstPair)
	}
	if err := database.Pool.QueryRow(ctx, `
		SELECT count(*)
		FROM audit_log
		WHERE tree_id = $1
		  AND action = 'parent_child_relation.deleted'
	`, treeA).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("relation deletion audit count = %d, error = %v", auditCount, err)
	}
}

func relationshipCreateTree(
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

func relationshipCreatePerson(
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

func relationshipCreateRelation(
	t *testing.T,
	ctx context.Context,
	relationService *service.Service,
	ownerID uuid.UUID,
	treeID uuid.UUID,
	parentID uuid.UUID,
	childID uuid.UUID,
	relationType string,
) service.Result {
	t.Helper()
	created, err := relationService.Create(ctx, service.CreateCommand{
		ActorUserID: ownerID,
		TreeID:      treeID,
		Values: domain.CreateValues{
			ParentPersonID: parentID,
			ChildPersonID:  childID,
			RelationType:   relationType,
			Confidence:     domain.ConfidenceConfirmed,
		},
	})
	if err != nil {
		t.Fatalf("create relation: %v", err)
	}
	return created
}
