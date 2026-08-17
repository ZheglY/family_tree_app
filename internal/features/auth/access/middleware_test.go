package access

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	coremiddleware "github.com/ZheglY/family_tree_app/internal/core/transport/http/middleware"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestRequireAccessAddsPrincipalToContext(t *testing.T) {
	verifier, privateKey, now := testVerifier(t)
	userID := uuid.New()
	sessionID := uuid.New()
	rawToken := signToken(t, privateKey, now, userID, sessionID, jwt.SigningMethodEdDSA)
	called := false
	handler := coremiddleware.ChainMiddleware(
		RequireAccess(verifier)(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			principal, ok := PrincipalFromContext(request.Context())
			if !ok || principal.UserID != userID || principal.SessionID != sessionID {
				t.Fatalf("principal = %#v, ok = %t", principal, ok)
			}
			called = true
		})),
		coremiddleware.RequestID(),
		coremiddleware.Logger(logger.NewNop()),
	)
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+rawToken)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if !called {
		t.Fatal("protected handler was not called")
	}
}

func TestRequireAccessRejectsMissingBearerToken(t *testing.T) {
	verifier, _, _ := testVerifier(t)
	handler := coremiddleware.ChainMiddleware(
		RequireAccess(verifier)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("protected handler was called")
		})),
		coremiddleware.RequestID(),
		coremiddleware.Logger(logger.NewNop()),
	)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/protected", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	if recorder.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("WWW-Authenticate = %q", recorder.Header().Get("WWW-Authenticate"))
	}
}
