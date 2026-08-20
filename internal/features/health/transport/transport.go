package healthhttp

import (
	"context"
	"net/http"

	"github.com/ZheglY/family_tree_app/internal/core/transport/http/server"
)

type HealthHandler struct {
	healthService HealthService
}

type HealthService interface {
	Live(context.Context) error
	Ready(context.Context) error
}

func NewHealthHTTPHandler(
	healthService HealthService,
) *HealthHandler {
	return &HealthHandler{
		healthService: healthService,
	}
}

// Роутер который необходимо зарегестрировать в апи роутере
func (h *HealthHandler) Routes() []server.Route {
	return []server.Route{
		{
			Method:  http.MethodGet,
			Path:    "/health/live",
			Handler: h.Live,
		},
		{
			Method:  http.MethodGet,
			Path:    "/health/ready",
			Handler: h.Ready,
		},
	}
}
