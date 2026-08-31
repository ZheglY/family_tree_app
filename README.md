# Family Tree API

Backend приватного сервиса семейных деревьев. Здесь находится браузерный HTTP API,
модель семейного графа, обработка медиа и экспортов, а также worker фоновых задач.
Учётные записи, пароли и сессии вынесены в отдельный
[Identity Service](https://github.com/ZheglY/family-tree-identity-microservice).

> Это backend-репозиторий. В нём пока нет пользовательского web-интерфейса для
> визуального редактирования дерева: после локального запуска доступны HTTP API и
> OpenAPI-контракт. Полный сценарий с реальными HTTP и gRPC вызовами покрывает
> автоматический E2E smoke.

## Как сервисы работают вместе

```text
Browser / frontend
        |
        | HTTPS / HTTP API
        v
Family Tree API :8080 ----------------------> PostgreSQL :5434
        |  |                                           |
        |  +-------------------------------------> MinIO / Yandex Object Storage
        |                                           |
        +-- gRPC :50051 --> Identity Service --> PostgreSQL :5433
        |
        +-- PostgreSQL job queue --> Family Tree Worker
```

| Компонент | Зона ответственности | Локальный адрес |
|---|---|---|
| Identity Service | регистрация, подтверждение email, пароли, JWT access tokens и refresh-сессии | gRPC `localhost:50051`, PostgreSQL `localhost:5433` |
| Family Tree API | публичный `/api/v1`, права на деревья, персоны, связи, союзы, медиа и экспорты | HTTP `localhost:8080`, PostgreSQL `localhost:5434` |
| Family Tree Worker | обработка медиа, создание экспортов и очистка просроченных результатов | без публичного HTTP API |
| MinIO | локальное S3-совместимое приватное хранилище медиа и экспортов | API `localhost:9000`, console `localhost:9001` |
| Redis | локальный вариант распределённого rate limit; не обязателен при development-настройке `memory` | `localhost:6379` |

Family API не подключается к базе Identity. Оно получает UUID пользователя и
выполняет auth-операции через версионированный контракт `identity.v1`; access token
проверяется локально по публичному Ed25519-ключу Identity. Поэтому для полного
локального запуска нужны оба репозитория, две отдельные PostgreSQL базы и worker.

## Быстрый совместный запуск

### Требования

- Go `1.26.2` или совместимая версия из `go.mod` обоих репозиториев;
- Docker Desktop / Docker Engine с Compose v2;
- два репозитория в одном родительском каталоге:

  ```text
  projects/
  ├── family_tree_app/
  └── family-tree-identity-microservice/
  ```

Команды ниже рассчитаны на PowerShell и запускаются в трёх терминалах.
Параметры development по умолчанию уже совпадают с Compose-конфигурациями, поэтому
для первого запуска секреты настраивать не нужно.

### 1. Запустить Identity Service

Откройте первый терминал в каталоге `family-tree-identity-microservice`:

```powershell
docker compose up -d --wait identity-postgres
go run ./cmd/migrate up
go run ./cmd/identity-service
```

Identity начнёт слушать `localhost:50051`. В development он создаёт временный
Ed25519-ключ при отсутствии `IDENTITY_ACCESS_TOKEN_PRIVATE_KEY_BASE64`; после
перезапуска ранее выданные access tokens станут недействительными. Ссылки
подтверждения email и восстановления пароля выводятся в его структурированный лог
только для development.

### 2. Запустить Family Tree API

Откройте второй терминал в каталоге `family_tree_app`:

```powershell
docker compose up -d --wait family-postgres auth-redis media-s3
go run ./cmd/migrate up
go run ./cmd/family_tree_app
```

При старте API подключается к `localhost:50051`, получает ключ проверки access token
у Identity и создаёт private bucket `family-tree-media` в локальном MinIO.

### 3. Запустить worker

В третьем терминале, из `family_tree_app`:

```powershell
go run ./cmd/worker
```

Worker обязателен для полного сценария: только он переводит загрузки медиа в `ready`,
создаёт варианты изображений и выполняет JSON/ZIP/PDF/PNG/SVG/GEDCOM/GEDZIP export
jobs.

### Проверить запуск

```powershell
Invoke-RestMethod http://localhost:8080/health/live
Invoke-RestMethod http://localhost:8080/health/ready
```

Оба ответа должны содержать `response: OK`. Спецификация публичного API находится в
[api/openapi.yaml](api/openapi.yaml). Для проверки регистрации используйте
`POST /api/v1/auth/register`, затем возьмите одноразовый token из development-лога
Identity и вызовите `POST /api/v1/auth/verify-email`.

## Настройка окружения

[.env.example](.env.example) описывает все переменные Family API, а
[`Identity .env.example`](https://github.com/ZheglY/family-tree-identity-microservice/blob/main/.env.example)
— переменные Identity. Это reference-файлы: процессы читают **переменные окружения**
и не подгружают `.env` автоматически.

Например, чтобы изменить gRPC-адрес Identity для процесса Family API, перед запуском
во втором терминале задайте:

```powershell
$env:IDENTITY_GRPC_ADDR="127.0.0.1:50051"
$env:IDENTITY_GRPC_TLS_ENABLED="false"
go run ./cmd/family_tree_app
```

Для production обязательны отдельные PostgreSQL и private Yandex Object Storage,
постоянный ключ Identity, TLS и аутентифицированный service-to-service transport.
Не используйте development Compose credentials и отключённый TLS вне локальной среды.

## Автоматический cross-service smoke

Сценарий собирает временные binaries, применяет миграции обеих test-баз, запускает
Identity, Family API и worker, а затем проверяет регистрацию, email verification,
login/refresh/logout, дерево, граф, медиа, экспорты, сессии, rate limit и метрики.

Сначала поднимите только зависимости. Не запускайте вручную API или Identity на
портах `50051`, `18080`, `19090`, `19091`: сценарий займёт их сам.

```powershell
cd ..\family-tree-identity-microservice
docker compose up -d --wait identity-postgres

cd ..\family_tree_app
docker compose up -d --wait family-postgres media-s3
.\scripts\e2e-auth-smoke.ps1 `
  -IdentityRepository '..\family-tree-identity-microservice'
```

## Обычные команды разработки

### Миграции Family Tree

```powershell
go run ./cmd/migrate up
go run ./cmd/migrate version
go run ./cmd/migrate down 1
```

### Тесты Family Tree

```powershell
# Unit и API tests без внешних зависимостей.
go test ./...
go vet ./...

# PostgreSQL + MinIO integration tests.
$env:FAMILY_TEST_DATABASE_URL="postgres://family_tree:family_tree@localhost:5434/family_tree_test?sslmode=disable"
$env:S3_TEST_ENDPOINT="http://localhost:9000"
go test -count=1 ./...
```

### Остановить локальные зависимости

```powershell
docker compose down
```

Команда останавливает контейнеры, но сохраняет named volumes с локальными данными.

## Документация

- [Концепция продукта](docs/PROJECT_CONCEPT.md)
- [Архитектурный план](docs/BACKEND_ROADMAP.md)
- [Почему Identity вынесен в отдельный сервис](docs/ADR-001-IDENTITY-SERVICE.md)
- [HTTP auth API и gRPC-интеграция](docs/AUTH_HTTP_API.md)
- [Trees HTTP API](docs/TREES_HTTP_API.md), [Persons API](docs/PERSONS_HTTP_API.md), [Relationships and graph API](docs/RELATIONSHIPS_HTTP_API.md), [Family unions API](docs/UNIONS_HTTP_API.md)
- [Media и private S3](docs/MEDIA_HTTP_API.md), [экспорт](docs/EXPORT_HTTP_API.md), [backup/restore](docs/BACKUP_RESTORE.md)
- [Наблюдаемость](docs/OBSERVABILITY.md), [нагрузка и E2E](docs/LOAD_AND_E2E.md), [staging/release runbook](docs/RELEASE_RUNBOOK.md)
