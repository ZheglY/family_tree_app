package server

import (
	"fmt"
	"net/http"

	"github.com/ZheglY/family_tree_app/internal/core/transport/http/middleware"
)

type ApiVersion string

var (
	ApiVersion1 = ApiVersion("v1")
)

type APIVersionRouter struct {
	*http.ServeMux
	apiVersion ApiVersion
	middleware []middleware.Middleware
}

func NewAPIVersionRouter(
	apiVersion ApiVersion,
	middlewares ...middleware.Middleware,
) *APIVersionRouter {
	return &APIVersionRouter{
		ServeMux:   http.NewServeMux(),
		apiVersion: apiVersion,
		middleware: middlewares,
	}
}

/*
Регистрируем роуты циклом проходясь по роутам конкретной фичи

Здесь конечным handler является не GetHealth, а весь APIVersionRouter.
То есть обёртка мидлварь применяется ко всем маршрутам этой версии:
middleware API v1
    ↓
APIVersionRouter v1
    ├── GET /health
    ├── GET /users
    └── POST /users
*/
func (r *APIVersionRouter) RegisterRoutes(routes ...Route) {
	for _, route := range routes {
		pattern := fmt.Sprintf("%s %s", route.Method, route.Path)

		r.Handle(pattern, route.WithMiddleware())
	}
}

/*
Оборачивает все пути внутри APIVersionRouter

Route.WithMiddleware()            → один endpoint
APIVersionRouter.WithMiddleware() → вся версия API
*/
func (h *APIVersionRouter) WithMiddleware() http.Handler {
	return middleware.ChainMiddleware(
		h,
		h.middleware...,
	)
}
