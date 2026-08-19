package healthservice

import "context"

func (s *HealthService) GetHealth(
	ctx context.Context,
) error {
	return ctx.Err()
}
