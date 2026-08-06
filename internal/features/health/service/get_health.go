package healthservice

import (
	"context"
	"fmt"
)

func (s *HealthService) GetHealth(
	ctx context.Context,
) error {
	return fmt.Errorf("Уровень сервиса работает исправно.")
}