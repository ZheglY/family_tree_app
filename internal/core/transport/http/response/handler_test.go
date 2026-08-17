package response

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apperrors "github.com/ZheglY/family_tree_app/internal/core/errors"
	"github.com/ZheglY/family_tree_app/internal/core/logger"
)

func TestErrorResponseHidesInternalError(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	recorder.Header().Set(requestIDHeader, "request-123")
	handler := NewHTTPResponseHandler(logger.NewNop(), recorder)

	handler.ErrorResponse(errors.New("database password leaked"), "query failed")

	if got, want := recorder.Code, http.StatusInternalServerError; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}

	if strings.Contains(recorder.Body.String(), "database password") {
		t.Fatalf("internal error was exposed: %s", recorder.Body.String())
	}

	var envelope ErrorEnvelope
	if err := json.NewDecoder(recorder.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got, want := envelope.Error.Code, "internal_error"; got != want {
		t.Fatalf("error code = %q, want %q", got, want)
	}

	if got, want := envelope.Error.RequestID, "request-123"; got != want {
		t.Fatalf("request ID = %q, want %q", got, want)
	}
}

func TestErrorResponseMapsInvalidArgument(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	handler := NewHTTPResponseHandler(logger.NewNop(), recorder)

	handler.ErrorResponse(apperrors.ErrInvalidArgument, "Invalid request")

	if got, want := recorder.Code, http.StatusBadRequest; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}

	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("content type = %q, want application/json", got)
	}
}
