package healthrepository

import (
	"context"
	"fmt"
)

func (r *HealthRepository) GetHealth(
	ctx context.Context,
) error {
	return fmt.Errorf("Уровень сервиса работает исправно.")
}