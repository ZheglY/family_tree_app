package healthhttp

import (
	"context"
	"net/http"

	"github.com/ZheglY/family_tree_app/internal/core/transport/http/server"
)

type HealthHadler struct {
	healthService HealthService
}

type HealthService interface {
	GetHealth(
		ctx context.Context,
	) error
}

func NewHealthHTTPHandler(
	healthService HealthService,
) *HealthHadler {
	return &HealthHadler{
		healthService: healthService,
	}
}

// Роутер который необходимо зарегестрировать в апи роутере
func (h *HealthHadler) Routes() []server.Route {
	return []server.Route{
		{
			Method:  http.MethodGet,
			Path:    "/health/live",
			Handler: h.GetHealth,
		},
	}
}
