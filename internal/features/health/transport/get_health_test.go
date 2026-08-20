package healthhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apperrors "github.com/ZheglY/family_tree_app/internal/core/errors"
	"github.com/ZheglY/family_tree_app/internal/core/logger"
)

type healthServiceStub struct {
	liveErr  error
	readyErr error
}

func (s healthServiceStub) Live(context.Context) error {
	return s.liveErr
}

func (s healthServiceStub) Ready(context.Context) error {
	return s.readyErr
}

func TestGetHealthReturnsOK(t *testing.T) {
	t.Parallel()

	handler := NewHealthHTTPHandler(healthServiceStub{})
	request := requestWithLogger(httptest.NewRequest(http.MethodGet, "/health/live", nil))
	recorder := httptest.NewRecorder()

	handler.Live(recorder, request)

	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}

	if !strings.Contains(recorder.Body.String(), `"response":"OK"`) {
		t.Fatalf("unexpected response body: %s", recorder.Body.String())
	}
}

func TestGetHealthStopsAfterError(t *testing.T) {
	t.Parallel()

	handler := NewHealthHTTPHandler(healthServiceStub{liveErr: errors.New("health failed")})
	request := requestWithLogger(httptest.NewRequest(http.MethodGet, "/health/live", nil))
	recorder := httptest.NewRecorder()

	handler.Live(recorder, request)

	if got, want := recorder.Code, http.StatusInternalServerError; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}

	if strings.Contains(recorder.Body.String(), `"response":"OK"`) {
		t.Fatalf("success response was written after error: %s", recorder.Body.String())
	}
}

func TestReadyReturnsServiceUnavailable(t *testing.T) {
	t.Parallel()
	handler := NewHealthHTTPHandler(healthServiceStub{readyErr: apperrors.ErrServiceUnavailable})
	request := requestWithLogger(httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	recorder := httptest.NewRecorder()

	handler.Ready(recorder, request)

	if got, want := recorder.Code, http.StatusServiceUnavailable; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}
}

func requestWithLogger(request *http.Request) *http.Request {
	ctx := logger.ToContext(request.Context(), logger.NewNop())
	return request.WithContext(ctx)
}
