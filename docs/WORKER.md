# PostgreSQL-backed worker

Статус: реализованный контракт Этапа 9. Worker запускается отдельным процессом `go run ./cmd/worker`, но использует те же PostgreSQL и приватный S3 bucket, что и Family API.

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
- `MEDIA_CLEANUP_BATCH_SIZE=100`.

Heartbeat должен быть чаще половины lease. Один процесс сейчас выполняет задания последовательно; горизонтальное масштабирование достигается запуском нескольких worker-процессов с разными `WORKER_ID`.
