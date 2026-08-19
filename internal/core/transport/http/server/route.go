package server

import (
	"net/http"

	"github.com/ZheglY/family_tree_app/internal/core/transport/http/middleware"
)

type Route struct {
	Method     string
	Path       string
	Handler    http.HandlerFunc
	Middleware []middleware.Middleware
}

/*
Оборачивает один конечный handler

Route.WithMiddleware()            → один endpoint
APIVersionRouter.WithMiddleware() → вся версия API

Если у маршрута есть middleware:
GET /health → RouteMiddleware → GetHealth
Если middleware нет:
GET /health → GetHealth
*/
func (r *Route) WithMiddleware() http.Handler {
	return middleware.ChainMiddleware(
		r.Handler,
		r.Middleware...,
	)
}
