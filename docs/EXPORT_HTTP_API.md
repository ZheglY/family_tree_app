# Export HTTP API

Статус: реализованы `json_backup`, `zip_backup` schema v1 и проверяемый offline restore ZIP в чистые PostgreSQL/S3. Все HTTP-пути имеют префикс `/api/v1`, требуют Bearer access token и активного membership дерева. Публичного HTTP import endpoint нет.

## Права и состояния

- `owner` и `editor` могут создавать экспорт;
- получить status/download и удалить результат может requester либо `owner`;
- `owner` видит всю историю дерева, остальные участники — только собственные задания;
- outsider и участник без права на конкретное задание получают `404`, чтобы не раскрывать его существование;
- состояния: `queued`, `running`, `completed`, `failed`, `expired`;
- готовый результат хранится в private S3, по умолчанию семь дней, и выдаётся только короткоживущей подписанной ссылкой.

## 1. Создать экспорт

`POST /trees/{treeID}/exports`

```json
{
  "client_request_id": "977874c3-a08b-4748-906d-94d3acee334b",
  "format": "json_backup"
}
```

Новый запрос возвращает `201 Created`. Идемпотентный повтор с тем же `client_request_id` возвращает `200 OK`, `created: false` и тот же `export.id`. Повтор с отличающимися параметрами возвращает `409 Conflict`.

```json
{
  "export": {
    "id": "85972d5b-8da5-487e-b030-e83196ce15aa",
    "tree_id": "5c29f22c-75d7-44ef-947b-0497524624ad",
    "format": "json_backup",
    "schema_version": 1,
    "status": "queued",
    "progress": 0,
    "created_at": "2026-08-21T12:00:00Z"
  },
  "created": true,
  "access": {"role": "editor", "status": "active"}
}
```

Создание `export_jobs`, `export.generate` и audit entry выполняется одной PostgreSQL-транзакцией.

Поддерживаемые форматы:

- `json_backup` — один JSON manifest без бинарных файлов, MIME результата `application/json`;
- `zip_backup` — `manifest.json`, `checksums.sha256`, доступные оригиналы и варианты активных media в состояниях `uploaded`, `processing` и `ready`, MIME результата `application/zip`.

В ZIP используются только UUID-based пути `media/{mediaID}/...`; пользовательское имя файла не становится ZIP path. Перед упаковкой worker повторно проверяет размер и SHA-256 каждого объекта. Суммарный входной объём ограничен `EXPORT_MAX_ARCHIVE_BYTES` (по умолчанию 256 MiB); превышение завершает задание с `error_code: archive_too_large`.

## 2. История и статус

- `GET /trees/{treeID}/exports?cursor=...&limit=20`;
- `GET /trees/{treeID}/exports/{exportID}`.

История использует cursor pagination, максимальный `limit` — `100`. API не раскрывает постоянный S3 object key. Готовая запись содержит `result_mime_type`, размер, SHA-256, `finished_at` и `expires_at`; при ошибке — безопасный `error_code` без внутренних деталей.

## 3. Скачать результат

`GET /trees/{treeID}/exports/{exportID}/download`

Доступен только для непросроченного `completed` задания. Для остальных состояний возвращается `409 Conflict`.

```json
{
  "export": {"id": "85972d5b-8da5-487e-b030-e83196ce15aa", "status": "completed"},
  "download": {
    "url": "https://storage.example/...?X-Amz-Signature=...",
    "method": "GET",
    "headers": {},
    "expires_at": "2026-08-21T12:10:00Z"
  },
  "access": {"role": "owner", "status": "active"}
}
```

Скачивание фиксируется в audit log. Клиент должен использовать указанные method и headers до `download.expires_at`.

## 4. Удалить результат

`DELETE /trees/{treeID}/exports/{exportID}` возвращает `204 No Content` и идемпотентно переводит задание в `expired`. Приватный объект удаляет worker; повторный download сразу возвращает `409`, даже если физическая очистка ещё выполняется.

## JSON manifest schema v1

Корневой объект содержит `schema`, `export`, `tree` и массивы `members`, `persons`, `person_names`, `parent_child_relations`, `unions`, `union_members`, `media_assets`, `media_variants`, `person_media`.

Manifest — консистентный repeatable-read snapshot. В него входят мягко удалённые доменные записи, необходимые restore. Не входят S3 object keys, audit log, background jobs и секреты. В `zip_backup` поля `archive_path` связывают активные `uploaded`/`processing`/`ready` media с файлами внутри ZIP; в `json_backup` эти поля отсутствуют.

Offline-команда `go run ./cmd/restore-backup <archive.zip>` строго проверяет schema, canonical paths, checksum set, размеры и ссылочную целостность, затем восстанавливает записи транзакционно и компенсирует собственные S3 uploads при ошибке. Clean PostgreSQL + real MinIO restore drill покрыт integration-тестом. Подробности — в [BACKUP_RESTORE.md](BACKUP_RESTORE.md). Публичный пользовательский import потребует отдельного контракта mapping/conflict preview и не считается частью текущего HTTP API.
