package middleware

import (
	"net/http"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
)

/*
Middleware — это тип функции, которая принимает handler 
и возвращает новый handler.
*/
type Middleware func(http.Handler) http.Handler

func Logger(log *logger.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(
			func(rw http.ResponseWriter, r *http.Request) {
			ctx := logger.ToContext(
				r.Context(),
				log,
			)
			
			// r.WithContext(ctx) - создаёт копию HTTP-запроса с новым контекстом
			requestWithLogger := r.WithContext(ctx)
			// Копия запроса передаётся следующему handler
			next.ServeHTTP(rw, requestWithLogger)
		})
	}
}