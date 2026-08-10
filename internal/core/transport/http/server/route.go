package server

import (
	"net/http"

	"github.com/ZheglY/family_tree_app/internal/core/transport/http/middleware"
)

type Route struct {
	Method string
	Path string
	Handler http.HandlerFunc
	Middleware []middleware.Middleware
}

func (r *Route) WithMiddleware() http.Handler {
	return middleware.ChainMiddleware(
		r.Handler,
		r.Middleware...,
	)
}