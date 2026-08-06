package healthrepository

type HealthRepository struct {
	pool string
}

func NewHealthRepository(
	pool string,
) *HealthRepository{
	return &HealthRepository{
		pool: pool,
	}
}

