package healthrepository

import "context"

func (r *HealthRepository) GetHealth(
	ctx context.Context,
) error {
	if r.ping == nil {
		return nil
	}
	return r.ping(ctx)
}
