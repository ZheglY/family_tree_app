package healthrepository

import "context"

func (r *HealthRepository) GetHealth(
	ctx context.Context,
) error {
	for _, check := range r.checks {
		if check == nil {
			continue
		}
		if err := check(ctx); err != nil {
			return err
		}
	}
	return nil
}
