package healthservice

import (
	"context"
	"errors"
	"testing"

	apperrors "github.com/ZheglY/family_tree_app/internal/core/errors"
)

type healthRepositoryStub struct {
	err error
}

func (s healthRepositoryStub) GetHealth(context.Context) error {
	return s.err
}

func TestLiveDoesNotDependOnPostgreSQL(t *testing.T) {
	t.Parallel()
	service := NewHealthService(healthRepositoryStub{err: errors.New("database unavailable")})
	if err := service.Live(context.Background()); err != nil {
		t.Fatalf("Live() error = %v", err)
	}
}

func TestReadyMapsPostgreSQLFailureToServiceUnavailable(t *testing.T) {
	t.Parallel()
	service := NewHealthService(healthRepositoryStub{err: errors.New("database unavailable")})
	err := service.Ready(context.Background())
	if !errors.Is(err, apperrors.ErrServiceUnavailable) {
		t.Fatalf("Ready() error = %v, want ErrServiceUnavailable", err)
	}
}
