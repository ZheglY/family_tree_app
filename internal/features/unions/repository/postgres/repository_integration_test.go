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
	relationdomain "github.com/ZheglY/family_tree_app/internal/features/relationships/domain"
	relationpostgres "github.com/ZheglY/family_tree_app/internal/features/relationships/repository/postgres"
	relationservice "github.com/ZheglY/family_tree_app/internal/features/relationships/service"
	treedomain "github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	treepostgres "github.com/ZheglY/family_tree_app/internal/features/trees/repository/postgres"
	"github.com/ZheglY/family_tree_app/internal/features/unions/domain"
	unionpostgres "github.com/ZheglY/family_tree_app/internal/features/unions/repository/postgres"
	unionservice "github.com/ZheglY/family_tree_app/internal/features/unions/service"
	"github.com/ZheglY/family_tree_app/internal/testdatabase"
	"github.com/ZheglY/family_tree_app/migrations"
	"github.com/google/uuid"
)

func TestUnionRepositoryLifecyclePartnerGraphAndTenantIsolation(t *testing.T) {
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
	unionRepository := unionpostgres.New(database.Pool)
	unionService := unionservice.New(unionRepository, treeRepository)
	relationService := relationservice.New(relationpostgres.New(database.Pool), treeRepository)
	ownerA := uuid.New()
	ownerB := uuid.New()
	viewer := uuid.New()
	outsider := uuid.New()
	treeA := unionCreateTree(t, ctx, treeRepository, ownerA, "Tree A")
	treeB := unionCreateTree(t, ctx, treeRepository, ownerB, "Tree B")
	firstPersonID := unionCreatePerson(t, ctx, personService, ownerA, treeA, "Анна")
	secondPersonID := unionCreatePerson(t, ctx, personService, ownerA, treeA, "Борис")
	thirdPersonID := unionCreatePerson(t, ctx, personService, ownerA, treeA, "Вера")
	crossTreePersonID := unionCreatePerson(t, ctx, personService, ownerB, treeB, "Чужая персона")

	_, err = unionService.Create(ctx, unionservice.CreateCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		Values: domain.CreateValues{
			Type: domain.TypeMarriage,
			Members: []domain.MemberValues{
				{PersonID: firstPersonID},
				{PersonID: crossTreePersonID},
			},
		},
	})
	if !errors.Is(err, domain.ErrUnionNotFound) {
		t.Fatalf("cross-tree create error = %v, want ErrUnionNotFound", err)
	}
	var unionCount int
	if err := database.Pool.QueryRow(ctx, `
		SELECT count(*) FROM family_unions WHERE tree_id = $1
	`, treeA).Scan(&unionCount); err != nil || unionCount != 0 {
		t.Fatalf("union count after rolled back create = %d, error = %v", unionCount, err)
	}

	created, err := unionService.Create(ctx, unionservice.CreateCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		Values: domain.CreateValues{
			Type: domain.TypeMarriage,
			Note: "Метрическая запись",
			Members: []domain.MemberValues{
				{PersonID: firstPersonID, Role: "spouse"},
				{PersonID: secondPersonID, Role: "spouse"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	unionID := created.Aggregate.Union.ID
	if created.Aggregate.Union.Version != 1 || len(created.Aggregate.Members) != 2 {
		t.Fatalf("created aggregate = %#v", created.Aggregate)
	}
	var memberCount int
	if err := database.Pool.QueryRow(ctx, `
		SELECT count(*) FROM union_members WHERE tree_id = $1 AND union_id = $2
	`, treeA, unionID).Scan(&memberCount); err != nil || memberCount != 2 {
		t.Fatalf("member count = %d, error = %v", memberCount, err)
	}

	acceptedAt := time.Now().UTC()
	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO tree_members (
			tree_id, user_id, role, status, created_at, accepted_at
		) VALUES ($1, $2, 'viewer', 'active', $3, $3)
	`, treeA, viewer, acceptedAt); err != nil {
		t.Fatalf("insert viewer membership: %v", err)
	}
	if _, err := unionService.Get(ctx, viewer, treeA, unionID); err != nil {
		t.Fatalf("viewer Get() error = %v", err)
	}
	updatedNote := "Viewer mutation"
	if _, err := unionService.Update(ctx, unionservice.UpdateCommand{
		ActorUserID: viewer,
		TreeID:      treeA,
		UnionID:     unionID,
		Version:     1,
		Values:      domain.UpdateValues{Note: &updatedNote},
	}); !errors.Is(err, domain.ErrUnionAccessDenied) {
		t.Fatalf("viewer Update() error = %v, want ErrUnionAccessDenied", err)
	}
	if _, err := unionService.Get(ctx, outsider, treeA, unionID); !errors.Is(err, domain.ErrUnionNotFound) {
		t.Fatalf("outsider Get() error = %v, want ErrUnionNotFound", err)
	}

	unionType := domain.TypeCivilUnion
	endReason := "Архивное уточнение"
	updated, err := unionService.Update(ctx, unionservice.UpdateCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		UnionID:     unionID,
		Version:     1,
		Values: domain.UpdateValues{
			Type:      &unionType,
			EndReason: &endReason,
		},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Aggregate.Union.Version != 2 || updated.Aggregate.Union.Type != domain.TypeCivilUnion {
		t.Fatalf("updated union = %#v", updated.Aggregate.Union)
	}
	if _, err := unionService.Update(ctx, unionservice.UpdateCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		UnionID:     unionID,
		Version:     1,
		Values:      domain.UpdateValues{Note: &updatedNote},
	}); !errors.Is(err, domain.ErrUnionVersionConflict) {
		t.Fatalf("stale Update() error = %v", err)
	}

	withThirdMember, err := unionService.AddMember(ctx, unionservice.AddMemberCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		UnionID:     unionID,
		PersonID:    thirdPersonID,
		Role:        "member",
	})
	if err != nil {
		t.Fatalf("AddMember() error = %v", err)
	}
	if withThirdMember.Aggregate.Union.Version != 3 || len(withThirdMember.Aggregate.Members) != 3 {
		t.Fatalf("aggregate after AddMember() = %#v", withThirdMember.Aggregate)
	}
	if _, err := unionService.AddMember(ctx, unionservice.AddMemberCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		UnionID:     unionID,
		PersonID:    firstPersonID,
	}); !errors.Is(err, domain.ErrDuplicateUnionMember) {
		t.Fatalf("duplicate AddMember() error = %v", err)
	}
	if _, err := unionService.AddMember(ctx, unionservice.AddMemberCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		UnionID:     unionID,
		PersonID:    crossTreePersonID,
	}); !errors.Is(err, domain.ErrUnionNotFound) {
		t.Fatalf("cross-tree AddMember() error = %v", err)
	}
	if _, err := unionService.RemoveMember(ctx, unionservice.RemoveMemberCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		UnionID:     unionID,
		PersonID:    uuid.New(),
	}); !errors.Is(err, domain.ErrUnionMemberNotFound) {
		t.Fatalf("missing RemoveMember() error = %v", err)
	}

	withoutPartners, err := relationService.Graph(ctx, relationservice.GraphCommand{
		ActorUserID:    viewer,
		TreeID:         treeA,
		CenterPersonID: firstPersonID,
	})
	if err != nil {
		t.Fatalf("Graph() without partners error = %v", err)
	}
	if len(withoutPartners.Graph.Persons) != 1 || len(withoutPartners.Graph.Unions) != 0 {
		t.Fatalf("graph without partners = %#v", withoutPartners.Graph)
	}
	withPartners, err := relationService.Graph(ctx, relationservice.GraphCommand{
		ActorUserID:     viewer,
		TreeID:          treeA,
		CenterPersonID:  firstPersonID,
		IncludePartners: true,
	})
	if err != nil {
		t.Fatalf("Graph() with partners error = %v", err)
	}
	if len(withPartners.Graph.Persons) != 3 || len(withPartners.Graph.Unions) != 1 ||
		len(withPartners.Graph.UnionMembers) != 3 {
		t.Fatalf(
			"partner graph size = persons %d, unions %d, members %d",
			len(withPartners.Graph.Persons),
			len(withPartners.Graph.Unions),
			len(withPartners.Graph.UnionMembers),
		)
	}
	if withPartners.Graph.Unions[0].ID != unionID {
		t.Fatalf("partner graph union = %#v", withPartners.Graph.Unions[0])
	}

	withTwoMembers, err := unionService.RemoveMember(ctx, unionservice.RemoveMemberCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		UnionID:     unionID,
		PersonID:    thirdPersonID,
	})
	if err != nil {
		t.Fatalf("RemoveMember() error = %v", err)
	}
	if withTwoMembers.Aggregate.Union.Version != 4 || len(withTwoMembers.Aggregate.Members) != 2 {
		t.Fatalf("aggregate after RemoveMember() = %#v", withTwoMembers.Aggregate)
	}
	if _, err := unionService.RemoveMember(ctx, unionservice.RemoveMemberCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		UnionID:     unionID,
		PersonID:    secondPersonID,
	}); !errors.Is(err, domain.ErrUnionMemberLimit) {
		t.Fatalf("minimum RemoveMember() error = %v", err)
	}

	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO union_members (union_id, person_id, tree_id, role, created_at)
		VALUES ($1, $2, $3, '', now())
	`, unionID, crossTreePersonID, treeB); err == nil {
		t.Fatal("cross-tree union member insert succeeded")
	}
	if err := unionService.Delete(ctx, unionservice.MutationCommand{
		ActorUserID: ownerA,
		TreeID:      treeA,
		UnionID:     unionID,
		Version:     4,
		RequestID:   "union-delete",
		IPAddress:   "127.0.0.1",
	}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := unionService.Get(ctx, ownerA, treeA, unionID); !errors.Is(err, domain.ErrUnionNotFound) {
		t.Fatalf("Get() after delete error = %v", err)
	}
	graphAfterDelete, err := relationService.Graph(ctx, relationservice.GraphCommand{
		ActorUserID:     ownerA,
		TreeID:          treeA,
		CenterPersonID:  firstPersonID,
		IncludePartners: true,
	})
	if err != nil || len(graphAfterDelete.Graph.Persons) != 1 || len(graphAfterDelete.Graph.Unions) != 0 {
		t.Fatalf("graph after union delete = %#v, error = %v", graphAfterDelete.Graph, err)
	}
	var auditCount int
	if err := database.Pool.QueryRow(ctx, `
		SELECT count(*)
		FROM audit_log
		WHERE tree_id = $1 AND action = 'family_union.deleted'
	`, treeA).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("union deletion audit count = %d, error = %v", auditCount, err)
	}

	if _, err := relationService.Graph(ctx, relationservice.GraphCommand{
		ActorUserID:     outsider,
		TreeID:          treeA,
		CenterPersonID:  firstPersonID,
		IncludePartners: true,
	}); !errors.Is(err, relationdomain.ErrRelationNotFound) {
		t.Fatalf("outsider partner Graph() error = %v, want ErrRelationNotFound", err)
	}
}

func unionCreateTree(
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

func unionCreatePerson(
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
