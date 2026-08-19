package access

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	apperrors "github.com/ZheglY/family_tree_app/internal/core/errors"
	"github.com/ZheglY/family_tree_app/internal/core/logger"
	coremiddleware "github.com/ZheglY/family_tree_app/internal/core/transport/http/middleware"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/response"
)

type principalContextKey struct{}

func NewPrincipalContext(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

func RequireAccess(verifier *Verifier) coremiddleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, request *http.Request) {
			rawToken, ok := bearerToken(request.Header.Get("Authorization"))
			if !ok {
				writeUnauthorized(rw, request, ErrInvalidToken)
				return
			}

			principal, err := verifier.Verify(rawToken)
			if err != nil {
				writeUnauthorized(rw, request, err)
				return
			}

			ctx := NewPrincipalContext(request.Context(), principal)
			next.ServeHTTP(rw, request.WithContext(ctx))
		})
	}
}

func bearerToken(value string) (string, bool) {
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func writeUnauthorized(rw http.ResponseWriter, request *http.Request, err error) {
	rw.Header().Set("WWW-Authenticate", "Bearer")
	handler := response.NewHTTPResponseHandler(logger.FromContext(request.Context()), rw)
	handler.ErrorResponse(
		fmt.Errorf("%w: %v", apperrors.ErrUnauthorized, err),
		"Authentication is required",
	)
}
