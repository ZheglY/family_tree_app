package server

import (
	"fmt"
	"net/http"
)

type ApiVersion string

var (
	ApiVersion1 = ApiVersion("v1")
)

type APIVersionRouter struct {
	*http.ServeMux
	apiVersion ApiVersion
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

		r.Handle(pattern, route.Handler)
	}
}