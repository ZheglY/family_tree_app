package healthservice

import (
	"context"
	"fmt"

	apperrors "github.com/ZheglY/family_tree_app/internal/core/errors"
)

func (s *HealthService) Live(ctx context.Context) error {
	return ctx.Err()
}

func (s *HealthService) Ready(ctx context.Context) error {
	if err := s.healthRepository.GetHealth(ctx); err != nil {
		return fmt.Errorf("%w: PostgreSQL is unavailable: %v", apperrors.ErrServiceUnavailable, err)
	}
	return nil
}
