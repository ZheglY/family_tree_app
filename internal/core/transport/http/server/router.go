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
) *APIVersionRouter{
	return &APIVersionRouter{
		ServeMux: http.NewServeMux(),
		apiVersion: apiVersion,
	}
}

// Регистрируем роуты циклом проходясь по роутам конкретной фичи 
func (r *APIVersionRouter) RegisterRoutes(routes ...Route) {
	for _, route := range routes {
		pattern := fmt.Sprintf("%s %s", route.Method, route.Path)

		r.Handle(pattern, route.WithMiddleware())
	}
}

func (h *APIVersionRouter) WithMiddleware() http.Handler {
	return middleware.ChainMiddleware(
		h,
		h.middleware...,
	)
}