# Family Tree API

Backend многопользовательского сервиса семейных деревьев. Family API построен как модульный монолит на Go, использует отдельный gRPC Identity Service и собственную PostgreSQL для семейного домена.

Канонические документы: [концепция](docs/PROJECT_CONCEPT.md), [backend roadmap](docs/BACKEND_ROADMAP.md), [Trees HTTP API](docs/TREES_HTTP_API.md), [Persons HTTP API](docs/PERSONS_HTTP_API.md), [Relationships and graph API](docs/RELATIONSHIPS_HTTP_API.md), [Family unions API](docs/UNIONS_HTTP_API.md), [Media and private S3 API](docs/MEDIA_HTTP_API.md), [Export HTTP API](docs/EXPORT_HTTP_API.md), [восстановление ZIP backup](docs/BACKUP_RESTORE.md), [PostgreSQL worker](docs/WORKER.md).

## Локальный запуск

```powershell
docker compose up -d --wait family-postgres auth-redis media-s3
go run ./cmd/migrate up
$env:HTTP_ADDR=":8080"
$env:LOGGER_LEVEL="DEBUG"
$env:LOGGER_FOLDER=".\logs"
go run ./cmd/family_tree_app
```

После запуска API в отдельном терминале запустить worker:

```powershell
go run ./cmd/worker
```

`complete` атомарно создаёт задание обработки медиа. Только worker переводит файл в `ready`, проверив фактический SHA-256, magic bytes и декодирование изображения. Worker также создаёт JSON/ZIP-экспорты и удаляет просроченные результаты, поэтому для полного API должны работать оба процесса.

Identity Service должен быть запущен на адресе из `IDENTITY_GRPC_ADDR`. Переменные окружения и безопасные development defaults перечислены в `.env.example`.

Проверки состояния:

- `GET /health/live` — процесс принимает запросы;
- `GET /health/ready` — обязательные PostgreSQL и private S3 bucket доступны.

## Миграции

```powershell
go run ./cmd/migrate up
go run ./cmd/migrate version
go run ./cmd/migrate down 1
```

Проверяемое offline-восстановление `zip_backup` в подготовленные PostgreSQL и private S3 выполняется отдельной операторской командой:

```powershell
go run ./cmd/restore-backup .\family-tree-backup.zip
```

Команда сохраняет UUID дерева и отказывается перезаписывать существующее дерево или S3 objects. Требования и restore drill описаны в [документе восстановления](docs/BACKUP_RESTORE.md).

## Тесты

Unit-тесты не требуют внешних сервисов. PostgreSQL integration-тесты включаются отдельной переменной:

```powershell
$env:FAMILY_TEST_DATABASE_URL="postgres://family_tree:family_tree@localhost:5434/family_tree_test?sslmode=disable"
$env:S3_TEST_ENDPOINT="http://localhost:9000"
go test ./...
go vet ./...
```

