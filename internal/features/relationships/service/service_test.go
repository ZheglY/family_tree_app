package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/features/relationships/domain"
	treedomain "github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	"github.com/google/uuid"
)

type relationshipRepositoryStub struct {
	createCalled bool
	graphCalled  bool
}

func (r *relationshipRepositoryStub) CreateAcyclicEditable(context.Context, uuid.UUID, domain.ParentChildRelation) error {
	r.createCalled = true
	return nil
}

func (r *relationshipRepositoryStub) GetAccessible(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (domain.ParentChildRelation, error) {
	return domain.ParentChildRelation{}, domain.ErrRelationNotFound
}

func (r *relationshipRepositoryStub) UpdateEditable(context.Context, uuid.UUID, domain.ParentChildRelation) error {
	return nil
}

func (r *relationshipRepositoryStub) SoftDeleteEditable(context.Context, AuditMutation) error {
	return nil
}

func (r *relationshipRepositoryStub) LoadGraphAccessible(context.Context, GraphFilter) (domain.Graph, error) {
	r.graphCalled = true
	return domain.Graph{}, nil
}

type relationshipTreeRepositoryStub struct {
	access treedomain.TreeAccess
	err    error
}

func (r relationshipTreeRepositoryStub) GetAccessible(context.Context, uuid.UUID, uuid.UUID, bool) (treedomain.TreeAccess, error) {
	return r.access, r.err
}

func TestCreateRejectsViewerBeforeRepositoryMutation(t *testing.T) {
	t.Parallel()
	actorID := uuid.New()
	repository := &relationshipRepositoryStub{}
	service := New(repository, relationshipTreeRepositoryStub{
		access: relationshipTestTree(t, actorID, treedomain.RoleViewer),
	})
	_, err := service.Create(context.Background(), CreateCommand{
		ActorUserID: actorID,
		TreeID:      uuid.New(),
		Values: domain.CreateValues{
			ParentPersonID: uuid.New(),
			ChildPersonID:  uuid.New(),
		},
	})
	if !errors.Is(err, domain.ErrRelationAccessDenied) {
		t.Fatalf("Create() error = %v, want ErrRelationAccessDenied", err)
	}
	if repository.createCalled {
		t.Fatal("repository mutation was called for viewer")
	}
}

func TestGraphRejectsExcessiveDepthBeforeRepositoryQuery(t *testing.T) {
	t.Parallel()
	actorID := uuid.New()
	repository := &relationshipRepositoryStub{}
	service := New(repository, relationshipTreeRepositoryStub{
		access: relationshipTestTree(t, actorID, treedomain.RoleOwner),
	})
	_, err := service.Graph(context.Background(), GraphCommand{
		ActorUserID:      actorID,
		TreeID:           uuid.New(),
		CenterPersonID:   uuid.New(),
		AncestorsDepth:   MaxGraphDepth + 1,
		DescendantsDepth: 1,
	})
	if !errors.Is(err, domain.ErrInvalidRelation) {
		t.Fatalf("Graph() error = %v, want ErrInvalidRelation", err)
	}
	if repository.graphCalled {
		t.Fatal("repository graph query was called for invalid depth")
	}
}

func TestTreeNotFoundIsHiddenAsRelationNotFound(t *testing.T) {
	t.Parallel()
	service := New(
		&relationshipRepositoryStub{},
		relationshipTreeRepositoryStub{err: treedomain.ErrTreeNotFound},
	)
	_, err := service.Get(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrRelationNotFound) {
		t.Fatalf("Get() error = %v, want ErrRelationNotFound", err)
	}
}

func relationshipTestTree(t *testing.T, userID uuid.UUID, role string) treedomain.TreeAccess {
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
