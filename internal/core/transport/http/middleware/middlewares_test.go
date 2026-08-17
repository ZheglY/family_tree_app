package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/response"
)

func TestRequestIDGeneratesAndReturnsHeader(t *testing.T) {
	t.Parallel()

	handler := RequestID()(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get(requestIDHeader) == "" {
			t.Fatal("request ID was not added to request")
		}
		rw.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Header().Get(requestIDHeader) == "" {
		t.Fatal("request ID was not added to response")
	}
}

func TestRecoveryReturnsControlledInternalError(t *testing.T) {
	t.Parallel()

	handler := ChainMiddleware(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("boom")
		}),
		RequestID(),
		Logger(logger.NewNop()),
		Recovery(),
		Trace(),
	)

	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if got, want := recorder.Code, http.StatusInternalServerError; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}

	var envelope response.ErrorEnvelope
	if err := json.NewDecoder(recorder.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got, want := envelope.Error.Code, "internal_error"; got != want {
		t.Fatalf("error code = %q, want %q", got, want)
	}
}
