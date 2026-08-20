package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/features/auth/access"
	"github.com/ZheglY/family_tree_app/internal/features/trees/domain"
	"github.com/ZheglY/family_tree_app/internal/features/trees/service"
	"github.com/google/uuid"
)

type treeServiceStub struct {
	createCommand service.CreateCommand
	created       domain.TreeAccess
	getErr        error
}

func (s *treeServiceStub) Create(_ context.Context, command service.CreateCommand) (domain.TreeAccess, error) {
	s.createCommand = command
	return s.created, nil
}

func (s *treeServiceStub) List(context.Context, uuid.UUID) ([]domain.TreeAccess, error) {
	return nil, nil
}

func (s *treeServiceStub) Get(context.Context, uuid.UUID, uuid.UUID) (domain.TreeAccess, error) {
	return domain.TreeAccess{}, s.getErr
}

func (s *treeServiceStub) Update(context.Context, service.UpdateCommand) (domain.TreeAccess, error) {
	return domain.TreeAccess{}, nil
}

func (s *treeServiceStub) Delete(context.Context, service.MutationCommand) error {
	return nil
}

func (s *treeServiceStub) Restore(context.Context, service.MutationCommand) (domain.TreeAccess, error) {
	return domain.TreeAccess{}, nil
}

func TestCreateUsesAuthenticatedUserAsOwner(t *testing.T) {
	t.Parallel()
	userID := uuid.New()
	created := transportTestTree(t, userID)
	serviceStub := &treeServiceStub{created: created}
	handler := NewHandler(serviceStub, nil)
	request := httptest.NewRequest(
		http.MethodPost,
		"/trees",
		strings.NewReader(`{"name":"Dynasty"}`),
	)
	request = request.WithContext(access.NewPrincipalContext(
		logger.ToContext(request.Context(), logger.NewNop()),
		access.Principal{UserID: userID, SessionID: uuid.New()},
	))
	recorder := httptest.NewRecorder()

	handler.Create(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if serviceStub.createCommand.ActorUserID != userID {
		t.Fatalf("actor = %s, want %s", serviceStub.createCommand.ActorUserID, userID)
	}
	if !strings.Contains(recorder.Body.String(), `"role":"owner"`) {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestGetHidesTreeFromOutsider(t *testing.T) {
	t.Parallel()
	serviceStub := &treeServiceStub{getErr: domain.ErrTreeNotFound}
	handler := NewHandler(serviceStub, nil)
	request := httptest.NewRequest(http.MethodGet, "/trees/id", nil)
	request.SetPathValue("tree_id", uuid.NewString())
	request = request.WithContext(access.NewPrincipalContext(
		logger.ToContext(request.Context(), logger.NewNop()),
		access.Principal{UserID: uuid.New(), SessionID: uuid.New()},
	))
	recorder := httptest.NewRecorder()

	handler.Get(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"not_found"`) {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestUpdateMapsVersionConflict(t *testing.T) {
	t.Parallel()
	serviceStub := &versionConflictService{treeServiceStub: treeServiceStub{}}
	handler := NewHandler(serviceStub, nil)
	request := httptest.NewRequest(http.MethodPatch, "/trees/id", strings.NewReader(`{"version":1,"name":"Changed"}`))
	request.SetPathValue("tree_id", uuid.NewString())
	request = request.WithContext(access.NewPrincipalContext(
		logger.ToContext(request.Context(), logger.NewNop()),
		access.Principal{UserID: uuid.New(), SessionID: uuid.New()},
	))
	recorder := httptest.NewRecorder()

	handler.Update(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

type versionConflictService struct {
	treeServiceStub
}

func (s *versionConflictService) Update(context.Context, service.UpdateCommand) (domain.TreeAccess, error) {
	return domain.TreeAccess{}, domain.ErrTreeVersionConflict
}

func transportTestTree(t *testing.T, ownerID uuid.UUID) domain.TreeAccess {
	t.Helper()
	created, err := domain.NewFamilyTree(
		uuid.New(),
		ownerID,
		domain.CreateValues{Name: "Dynasty", Locale: "ru-RU", Timezone: "UTC"},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return created
}
