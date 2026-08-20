package healthrepository

import "context"

type HealthRepository struct {
	checks []func(context.Context) error
}

func NewHealthRepository(checks ...func(context.Context) error) *HealthRepository {
	return &HealthRepository{checks: checks}
}
