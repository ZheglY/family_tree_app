package healthrepository

import "context"

type HealthRepository struct {
	ping func(context.Context) error
}

func NewHealthRepository(ping func(context.Context) error) *HealthRepository {
	return &HealthRepository{ping: ping}
}
