package service

import (
	"context"
	"errors"
	"testing"
	"time"

	treedomain "github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	"github.com/ZheglY/family_tree_app/internal/features/unions/domain"
	"github.com/google/uuid"
)

type unionRepositoryStub struct {
	createCalled bool
}

func (r *unionRepositoryStub) CreateWithMembersEditable(context.Context, uuid.UUID, domain.Aggregate) error {
	r.createCalled = true
	return nil
}

func (r *unionRepositoryStub) GetAccessible(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (domain.Aggregate, error) {
	return domain.Aggregate{}, domain.ErrUnionNotFound
}

func (r *unionRepositoryStub) UpdateEditable(context.Context, uuid.UUID, domain.FamilyUnion) error {
	return nil
}

func (r *unionRepositoryStub) SoftDeleteEditable(context.Context, AuditMutation) error {
	return nil
}

func (r *unionRepositoryStub) AddMemberEditable(context.Context, uuid.UUID, domain.UnionMember) (domain.Aggregate, error) {
	return domain.Aggregate{}, nil
}

func (r *unionRepositoryStub) RemoveMemberEditable(context.Context, MemberMutation) (domain.Aggregate, error) {
	return domain.Aggregate{}, nil
}

type unionTreeRepositoryStub struct {
	access treedomain.TreeAccess
	err    error
}

func (r unionTreeRepositoryStub) GetAccessible(context.Context, uuid.UUID, uuid.UUID, bool) (treedomain.TreeAccess, error) {
	return r.access, r.err
}

func TestCreateRejectsViewerBeforeRepositoryMutation(t *testing.T) {
	t.Parallel()
	actorID := uuid.New()
	repository := &unionRepositoryStub{}
	service := New(repository, unionTreeRepositoryStub{
		access: unionTestTree(t, actorID, treedomain.RoleViewer),
	})
	_, err := service.Create(context.Background(), CreateCommand{
		ActorUserID: actorID,
		TreeID:      uuid.New(),
		Values: domain.CreateValues{Members: []domain.MemberValues{
			{PersonID: uuid.New()},
			{PersonID: uuid.New()},
		}},
	})
	if !errors.Is(err, domain.ErrUnionAccessDenied) {
		t.Fatalf("Create() error = %v, want ErrUnionAccessDenied", err)
	}
	if repository.createCalled {
		t.Fatal("repository mutation was called for viewer")
	}
}

func TestTreeNotFoundIsHiddenAsUnionNotFound(t *testing.T) {
	t.Parallel()
	service := New(
		&unionRepositoryStub{},
		unionTreeRepositoryStub{err: treedomain.ErrTreeNotFound},
	)
	_, err := service.Get(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrUnionNotFound) {
		t.Fatalf("Get() error = %v, want ErrUnionNotFound", err)
	}
}

func unionTestTree(t *testing.T, userID uuid.UUID, role string) treedomain.TreeAccess {
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
