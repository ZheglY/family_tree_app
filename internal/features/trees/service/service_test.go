package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	"github.com/google/uuid"
)

type repositoryStub struct {
	created        domain.TreeAccess
	accessible     domain.TreeAccess
	getErr         error
	updateCalled   bool
	deleteMutation AuditMutation
}

func (r *repositoryStub) CreateWithOwner(_ context.Context, access domain.TreeAccess) error {
	r.created = access
	return nil
}

func (r *repositoryStub) ListAccessible(context.Context, uuid.UUID) ([]domain.TreeAccess, error) {
	return []domain.TreeAccess{r.accessible}, nil
}

func (r *repositoryStub) GetAccessible(context.Context, uuid.UUID, uuid.UUID, bool) (domain.TreeAccess, error) {
	return r.accessible, r.getErr
}

func (r *repositoryStub) UpdateOwned(_ context.Context, _ uuid.UUID, tree domain.FamilyTree) (domain.FamilyTree, error) {
	r.updateCalled = true
	return tree, nil
}

func (r *repositoryStub) SoftDeleteOwned(_ context.Context, mutation AuditMutation) error {
	r.deleteMutation = mutation
	return nil
}

func (r *repositoryStub) RestoreOwned(context.Context, AuditMutation) (domain.FamilyTree, error) {
	return r.accessible.Tree, nil
}

func TestCreateUsesDefaultsAndCreatesOwnerMembership(t *testing.T) {
	t.Parallel()
	repository := &repositoryStub{}
	service := New(repository)
	treeID := uuid.New()
	ownerID := uuid.New()
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	service.newID = func() uuid.UUID { return treeID }
	service.now = func() time.Time { return now }

	created, err := service.Create(context.Background(), CreateCommand{
		ActorUserID: ownerID,
		Name:        "Family",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Tree.Locale != defaultLocale || created.Tree.Timezone != defaultTimezone {
		t.Fatalf("defaults = locale %q, timezone %q", created.Tree.Locale, created.Tree.Timezone)
	}
	if repository.created.Membership.Role != domain.RoleOwner ||
		repository.created.Membership.UserID != ownerID {
		t.Fatalf("created membership = %#v", repository.created.Membership)
	}
}

func TestUpdateRejectsNonOwnerBeforeRepositoryMutation(t *testing.T) {
	t.Parallel()
	actorID := uuid.New()
	repository := &repositoryStub{accessible: testAccess(t, actorID, domain.RoleViewer)}
	service := New(repository)
	name := "Changed"

	_, err := service.Update(context.Background(), UpdateCommand{
		ActorUserID: actorID,
		TreeID:      repository.accessible.Tree.ID,
		Version:     1,
		Name:        &name,
	})
	if !errors.Is(err, domain.ErrTreeAccessDenied) {
		t.Fatalf("Update() error = %v, want ErrTreeAccessDenied", err)
	}
	if repository.updateCalled {
		t.Fatal("repository mutation was called for viewer")
	}
}

func TestDeleteCarriesAuditContext(t *testing.T) {
	t.Parallel()
	actorID := uuid.New()
	repository := &repositoryStub{accessible: testAccess(t, actorID, domain.RoleOwner)}
	service := New(repository)
	auditID := uuid.New()
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	service.newID = func() uuid.UUID { return auditID }
	service.now = func() time.Time { return now }

	err := service.Delete(context.Background(), MutationCommand{
		ActorUserID: actorID,
		TreeID:      repository.accessible.Tree.ID,
		Version:     1,
		RequestID:   "request-123",
		IPAddress:   "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	mutation := repository.deleteMutation
	if mutation.AuditID != auditID || mutation.ActorUserID != actorID ||
		mutation.RequestID != "request-123" || mutation.IPAddress != "127.0.0.1" ||
		!mutation.OccurredAt.Equal(now) {
		t.Fatalf("audit mutation = %#v", mutation)
	}
}

func TestGetPreservesNotFoundForOutsider(t *testing.T) {
	t.Parallel()
	service := New(&repositoryStub{getErr: domain.ErrTreeNotFound})
	_, err := service.Get(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrTreeNotFound) {
		t.Fatalf("Get() error = %v, want ErrTreeNotFound", err)
	}
}

func testAccess(t *testing.T, actorID uuid.UUID, role string) domain.TreeAccess {
	t.Helper()
	access, err := domain.NewFamilyTree(
		uuid.New(),
		actorID,
		domain.CreateValues{Name: "Family", Locale: "ru-RU", Timezone: "UTC"},
		time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	access.Membership.Role = role
	return access
}
