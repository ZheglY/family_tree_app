# PostgreSQL-backed worker

Статус: реализованный контракт Этапа 9, JSON/ZIP backup, проверяемый offline restore и визуальный PDF/PNG/SVG export Этапа 10. Worker запускается отдельным процессом `go run ./cmd/worker`, но использует те же PostgreSQL и приватный S3 bucket, что и Family API.

## Гарантии очереди

Таблица `background_jobs` хранит payload и состояния `queued`, `running`, `failed`, `succeeded`, `dead`.

- claim выполняется через `FOR UPDATE SKIP LOCKED`, поэтому несколько worker-процессов не получают одно активное задание;
- при claim увеличивается `attempts`, записываются `locked_by` и ограниченный `lease_expires_at`;
- heartbeat продлевает lease; старый worker больше не может подтвердить результат после передачи lease другому процессу;
- ошибка переводит задание в `failed` и назначает exponential backoff;
- последняя неуспешная попытка переводит задание в `dead`;
- завершение процесса отменяет handler; неподтверждённое задание восстанавливается после истечения lease;
- `(kind, deduplication_key)` защищает от повторной постановки одного логического задания.

Payload хранится как `jsonb`, а проверка идемпотентного повтора сравнивает каноническое JSON-представление, не зависящее от порядка ключей.

## Задания медиа

### `media.process`

Создаётся в той же PostgreSQL-транзакции, которая переводит `MediaAsset` из `pending` в `uploaded`.

Handler идемпотентно выполняет:

1. переход `uploaded → processing`;
2. приватное скачивание с ограничением размера;
3. вычисление SHA-256 фактических байтов;
4. проверку JPEG/PNG/WebP/PDF по magic bytes;
5. полное декодирование изображения и ограничение в 80 млн пикселей;
6. создание JPEG `thumbnail` до 320 px и `preview` до 1600 px;
7. загрузку вариантов по детерминированным object keys;
8. транзакционную запись вариантов и переход `processing → ready`.

Повреждённый или подменённый файл становится `rejected`. Транзиентные ошибки S3/PostgreSQL повторяются; после исчерпания попыток media также помечается `rejected`, а job — `dead`.

Антивирусная проверка пока не реализована и остаётся production-hardening задачей.

### `media.cleanup`

Периодическое дедуплицированное задание создаётся самим worker-ом. Оно обрабатывает ограниченный batch:

- незавершённые `pending` старше `MEDIA_CLEANUP_PENDING_TTL`;
- `deleted` и `rejected` старше `MEDIA_CLEANUP_DELETED_RETENTION`;
- оригинал, известные варианты и детерминированные variant keys;
- окончательное удаление PostgreSQL metadata только после успешных S3 DELETE.

По умолчанию scheduler работает каждый час, pending TTL равен 24 часам, retention — 30 дням, batch — 100 записей.

## Задания экспорта

### `export.generate`

Создаётся атомарно с `ExportJob`. Handler делает repeatable-read снимок дерева, сериализует schema-versioned JSON manifest и вычисляет SHA-256. `json_backup` сохраняет только manifest по content-addressed ключу `trees/{treeID}/exports/{exportID}/manifest-{sha256}.json`. В manifest входят данные дерева, участники, персоны и имена, связи, союзы и media metadata, включая мягко удалённые доменные записи. Внутренние object keys, audit log и служебная очередь не экспортируются.

`zip_backup` дополняет тот же manifest доступными оригиналами и вариантами активных media в состояниях `uploaded`, `processing` и `ready`. Это позволяет не потерять файл, загруженный непосредственно перед созданием backup: после restore состояния `uploaded` и `processing` нормализуются в `uploaded` и получают новый `media.process`. Архив содержит безопасные UUID-based пути, `manifest.json` и `checksums.sha256`; каждый скачанный из S3 объект повторно проверяется по размеру и SHA-256. Результат сохраняется как `backup-{sha256}.zip`. Текущая реализация собирает архив в памяти и поэтому отклоняет входной объём выше `EXPORT_MAX_ARCHIVE_BYTES`; streaming/multipart остаётся следующим инфраструктурным улучшением.

`pdf`, `png` и `svg` используют один детерминированный layout активного графа: preferred names, parent-child relations и family unions. Партнёры выравниваются по поколению, а soft-deleted записи исключаются. PDF содержит embedded Unicode fonts, SVG не загружает внешние ресурсы, PNG кодируется worker-ом напрямую. Превышение `EXPORT_MAX_VISUAL_NODES`, `EXPORT_MAX_VISUAL_PIXELS` или общего byte limit завершает export с `visual_too_large` без бессмысленного retry.

Повторная обработка безопасна. После исчерпания retry доменное задание становится `failed`; удалённое во время генерации задание не может снова стать `completed`, а поздно загруженный объект удаляется.

### `export.cleanup` и `export.delete`

Результат доступен в течение `EXPORT_RESULT_TTL`. Периодический cleanup переводит просроченные записи в `expired`, удаляет приватный объект и очищает его S3 metadata из строки задания. Ручное удаление сразу переводит export в `expired` и ставит отдельное дедуплицированное `export.delete`.

## Конфигурация

Основные параметры:

- `WORKER_ID` — уникальный идентификатор процесса; по умолчанию создаётся из hostname и UUID;
- `WORKER_POLL_INTERVAL=1s`;
- `WORKER_LEASE_DURATION=30s`;
- `WORKER_HEARTBEAT_INTERVAL=10s`;
- `WORKER_RETRY_BASE_DELAY=2s`, `WORKER_RETRY_MAX_DELAY=15m`;
- `MEDIA_CLEANUP_INTERVAL=1h`;
- `MEDIA_CLEANUP_PENDING_TTL=24h`;
- `MEDIA_CLEANUP_DELETED_RETENTION=720h`;
- `MEDIA_CLEANUP_BATCH_SIZE=100`;
- `EXPORT_RESULT_TTL=168h`;
- `EXPORT_CLEANUP_INTERVAL=1h`;
- `EXPORT_CLEANUP_BATCH_SIZE=100`;
- `EXPORT_MAX_ARCHIVE_BYTES=268435456` (256 MiB);
- `EXPORT_MAX_VISUAL_NODES=250`;
- `EXPORT_MAX_VISUAL_PIXELS=32000000` (примерно 128 MiB RGBA до кодирования).

Heartbeat должен быть чаще половины lease. Один процесс сейчас выполняет задания последовательно; горизонтальное масштабирование достигается запуском нескольких worker-процессов с разными `WORKER_ID`.
