# OpenAPI contract and CI quality gates

Статус: первый hardening-срез Этапа 11 реализован. Проверяемый HTTP-контракт находится в [`api/openapi.yaml`](../api/openapi.yaml) и использует OpenAPI 3.1.1.

## Граница контракта

Спецификация описывает все маршруты, которые регистрирует `cmd/family_tree_app`:

- публичные `/health/live` и `/health/ready`;
- versioned API `/api/v1` для auth/account, trees, persons, relationships, graph, unions, media и exports;
- Bearer JWT и refresh-cookie security schemes;
- path/query параметры, JSON request bodies, успешные HTTP-статусы и единый error envelope;
- ограничения и enum текущей доменной модели, включая все семь export-форматов.

Contract-тест `api/openapi_test.go` загружает документ без внешних `$ref`, выполняет структурную валидацию и сравнивает пары `method + path` с фактическими `Routes()` активных transport-модулей. Он также проверяет уникальные `operationId`, обязательные summary/tags/responses и соответствие Bearer security реально установленным route middleware. Поэтому добавление, удаление или изменение защищённости endpoint требует атомарно обновить OpenAPI.

Локальная проверка:

```powershell
go test ./api -count=1
```

## GitHub Actions

Workflow `.github/workflows/backend-quality.yaml` запускается для push, pull request и вручную. Он имеет только `contents: read`, отменяет устаревший run той же ветки и разделён на два независимых job:

1. `static-and-race` проверяет `go mod tidy`, `gofmt`, workflow через actionlint, `go vet` и выполняет весь Go test suite с race detector; integration-тесты без environment variables корректно пропускаются.
2. `postgres-and-s3` поднимает PostgreSQL 17 и закреплённую версию MinIO, затем выполняет полный suite с `FAMILY_TEST_DATABASE_URL` и `S3_TEST_ENDPOINT`. Так clean-schema migrations, repositories, private S3, backup/restore и export pipelines выполняются в CI, а не только локально.

Локальный эквивалент интеграционного job:

```powershell
docker compose up -d --wait family-postgres media-s3
$env:FAMILY_TEST_DATABASE_URL='postgres://family_tree:family_tree@localhost:5434/family_tree_test?sslmode=disable'
$env:S3_TEST_ENDPOINT='http://localhost:9000'
go test ./... -count=1 -timeout=240s
go vet ./...
```

Следующие hardening-срезы: типизировать все success response schemas вместо общих JSON object там, где это ещё не сделано; добавить security headers/CORS/CSRF; затем metrics/logging audit, backup drills, нагрузочные/E2E проверки и staging release flow.
