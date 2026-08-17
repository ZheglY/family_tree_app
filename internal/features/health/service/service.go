package healthservice

import "context"

type HealthService struct {
	healthRepository HealthRepository
}

type HealthRepository interface {
	GetHealth(
		ctx context.Context,
	) error
}

func NewHealthService(
	healthRepository HealthRepository,
) *HealthService {
	return &HealthService{
		healthRepository: healthRepository,
	}
}
