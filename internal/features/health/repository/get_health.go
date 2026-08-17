package healthrepository

import "context"

func (r *HealthRepository) GetHealth(
	ctx context.Context,
) error {
	return ctx.Err()
}
