package middleware

import (
	"context"
	"net/http"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

/*
Middleware — это тип функции, которая принимает handler
и возвращает новый handler.
*/
type Middleware func(http.Handler) http.Handler

const (
	requestIDHeader = "X-Request-ID"
)

func ChainMiddleware(
	h http.Handler,
	m ...Middleware,
) http.Handler {
	if len(m) == 0 {
		return h
	}

	for i := len(m) - 1; i >= 0; i-- {
		h = m[i](h)
	}

	return h
}

func RequestID(ctx context.Context) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(
			func(rw http.ResponseWriter, r *http.Request) {
				requestID := r.Header.Get(requestIDHeader)
				if requestID == "" {
					requestID = uuid.NewString()
				}

				r.Header.Set(requestIDHeader, requestID)
				rw.Header().Set(requestIDHeader, requestID)

				next.ServeHTTP(rw, r)
			})
	}
}

func Logger(log *logger.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(
			func(rw http.ResponseWriter, r *http.Request) {
				requestID := r.Header.Get(requestIDHeader)
				l := log.With(
					zap.String("request_id", requestID),
					zap.String("request_id", r.URL.String()),
				)

				ctx := logger.ToContext(
					r.Context(),
					l,
				)

				// r.WithContext(ctx) - создаёт копию HTTP-запроса с новым контекстом
				requestWithLogger := r.WithContext(ctx)
				// Копия запроса передаётся следующему handler
				next.ServeHTTP(rw, requestWithLogger)
			})
	}
}
