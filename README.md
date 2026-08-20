# Family Tree API

Backend многопользовательского сервиса семейных деревьев. Family API построен как модульный монолит на Go, использует отдельный gRPC Identity Service и собственную PostgreSQL для семейного домена.

Канонические документы: [концепция](docs/PROJECT_CONCEPT.md), [backend roadmap](docs/BACKEND_ROADMAP.md), [Trees HTTP API](docs/TREES_HTTP_API.md), [Persons HTTP API](docs/PERSONS_HTTP_API.md).

## Локальный запуск

```powershell
docker compose up -d --wait family-postgres auth-redis
go run ./cmd/migrate up
$env:HTTP_ADDR=":8080"
$env:LOGGER_LEVEL="DEBUG"
$env:LOGGER_FOLDER=".\logs"
go run ./cmd/family_tree_app
```

Identity Service должен быть запущен на адресе из `IDENTITY_GRPC_ADDR`. Переменные окружения и безопасные development defaults перечислены в `.env.example`.

Проверки состояния:

- `GET /health/live` — процесс принимает запросы;
- `GET /health/ready` — обязательная PostgreSQL доступна.

## Миграции

```powershell
go run ./cmd/migrate up
go run ./cmd/migrate version
go run ./cmd/migrate down 1
```

## Тесты

Unit-тесты не требуют внешних сервисов. PostgreSQL integration-тесты включаются отдельной переменной:

```powershell
$env:FAMILY_TEST_DATABASE_URL="postgres://family_tree:family_tree@localhost:5434/family_tree_test?sslmode=disable"
go test ./...
go vet ./...
```

