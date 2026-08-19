package response

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResponseWriterCapturesExplicitStatus(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	writer := NewResponseWriter(recorder)

	writer.WriteHeader(http.StatusCreated)
	writer.WriteHeader(http.StatusInternalServerError)

	if got, want := writer.GetStatusCode(), http.StatusCreated; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}

	if got, want := recorder.Code, http.StatusCreated; got != want {
		t.Fatalf("recorded status code = %d, want %d", got, want)
	}
}

func TestResponseWriterCapturesImplicitOK(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	writer := NewResponseWriter(recorder)

	if _, err := writer.Write([]byte("ok")); err != nil {
		t.Fatalf("write response: %v", err)
	}

	if got, want := writer.GetStatusCode(), http.StatusOK; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}
}
