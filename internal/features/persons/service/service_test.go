package service

import (
	"context"
	"errors"
	"testing"
	"time"

	persondomain "github.com/ZheglY/family_tree_app/internal/features/persons/domain"
	treedomain "github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	"github.com/google/uuid"
)

type personRepositoryStub struct {
	createCalled bool
	listCalled   bool
}

func (r *personRepositoryStub) CreateEditable(context.Context, uuid.UUID, persondomain.Card) error {
	r.createCalled = true
	return nil
}

func (r *personRepositoryStub) ListAccessible(context.Context, ListFilter) ([]persondomain.Card, error) {
	r.listCalled = true
	return nil, nil
}

func (r *personRepositoryStub) GetAccessible(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, bool) (persondomain.Card, error) {
	return persondomain.Card{}, persondomain.ErrPersonNotFound
}

func (r *personRepositoryStub) UpdateEditable(context.Context, uuid.UUID, persondomain.Card) error {
	return nil
}

func (r *personRepositoryStub) SoftDeleteEditable(context.Context, AuditMutation) error {
	return nil
}

func (r *personRepositoryStub) RestoreEditable(context.Context, AuditMutation) (persondomain.Card, error) {
	return persondomain.Card{}, nil
}

type treeRepositoryStub struct {
	access treedomain.TreeAccess
	err    error
}

func (r treeRepositoryStub) GetAccessible(context.Context, uuid.UUID, uuid.UUID, bool) (treedomain.TreeAccess, error) {
	return r.access, r.err
}

func TestCreateRejectsViewerBeforePersonRepository(t *testing.T) {
	t.Parallel()
	actorID := uuid.New()
	personRepository := &personRepositoryStub{}
	service := New(personRepository, treeRepositoryStub{access: serviceTestTree(t, actorID, treedomain.RoleViewer)})

	_, err := service.Create(context.Background(), CreateCommand{
		ActorUserID: actorID,
		TreeID:      uuid.New(),
		Values: persondomain.CreateValues{
			Name: persondomain.NameValues{GivenName: "Анна"},
		},
	})
	if !errors.Is(err, persondomain.ErrPersonAccessDenied) {
		t.Fatalf("Create() error = %v, want ErrPersonAccessDenied", err)
	}
	if personRepository.createCalled {
		t.Fatal("person repository was called for viewer")
	}
}

func TestListRejectsInvalidCursorBeforeQuery(t *testing.T) {
	t.Parallel()
	actorID := uuid.New()
	personRepository := &personRepositoryStub{}
	service := New(personRepository, treeRepositoryStub{access: serviceTestTree(t, actorID, treedomain.RoleOwner)})

	_, err := service.List(context.Background(), ListCommand{
		ActorUserID: actorID,
		TreeID:      uuid.New(),
		Cursor:      "not-a-cursor",
	})
	if !errors.Is(err, persondomain.ErrInvalidListCursor) {
		t.Fatalf("List() error = %v, want ErrInvalidListCursor", err)
	}
	if personRepository.listCalled {
		t.Fatal("person repository was called for invalid cursor")
	}
}

func TestTreeNotFoundIsHiddenAsPersonNotFound(t *testing.T) {
	t.Parallel()
	service := New(
		&personRepositoryStub{},
		treeRepositoryStub{err: treedomain.ErrTreeNotFound},
	)
	_, err := service.Get(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, persondomain.ErrPersonNotFound) {
		t.Fatalf("Get() error = %v, want ErrPersonNotFound", err)
	}
}

func serviceTestTree(t *testing.T, userID uuid.UUID, role string) treedomain.TreeAccess {
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
