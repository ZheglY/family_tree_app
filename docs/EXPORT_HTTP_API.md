# Export HTTP API

Статус: реализованы `json_backup`, `zip_backup` schema v1, проверяемый offline restore ZIP, визуальные `pdf`/`png`/`svg`, переносимый `gedcom` и GEDZIP 7 с media. Все HTTP-пути имеют префикс `/api/v1`, требуют Bearer access token и активного membership дерева. Публичного HTTP import endpoint нет.

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
- `zip_backup` — `manifest.json`, `checksums.sha256`, доступные оригиналы и варианты активных media в состояниях `uploaded`, `processing` и `ready`, MIME результата `application/zip`;
- `pdf` — одностраничное векторное фамильное древо с embedded Unicode font, MIME `application/pdf`;
- `png` — полноразмерное raster image, MIME `image/png`;
- `svg` — масштабируемое vector image без внешних ресурсов, MIME `image/svg+xml`;
- `gedcom` — UTF-8 FamilySearch GEDCOM 7.0 с расширением `.ged`, MIME `text/vnd.familysearch.gedcom`;
- `gedzip` — `gedcom.ged` вместе с доступными приватными media, расширение `.gdz`, MIME `application/vnd.familysearch.gedcom+zip`.

В ZIP используются только UUID-based пути `media/{mediaID}/...`; пользовательское имя файла не становится ZIP path. Перед упаковкой worker повторно проверяет размер и SHA-256 каждого объекта. Суммарный входной объём ограничен `EXPORT_MAX_ARCHIVE_BYTES` (по умолчанию 256 MiB); превышение завершает задание с `error_code: archive_too_large`.

## Визуальный экспорт

PDF, PNG и SVG строятся из одного детерминированного scene/layout, поэтому содержат одинаковые поколения, подписи и связи. В первый визуальный срез входят:

- активные персоны с preferred name, полом и life status;
- активные parent-child relations;
- активные family unions, обозначенные отдельной пунктирной связью;
- выравнивание партнёров по одному поколению и направление поколений сверху вниз;
- спокойная историческая палитра: архивная бумага, бордовый контур и золотые акценты.

Soft-deleted записи, UUID, S3 keys, биографии, документы и приватные media bytes в изображение не попадают. Длинные имена безопасно переносятся и ограничиваются; пустое дерево получает отдельное сообщение. Результат остаётся в private S3 и выдаётся тем же requester/Owner download flow.

Worker ограничивает визуализацию через `EXPORT_MAX_VISUAL_NODES` (по умолчанию `250`), `EXPORT_MAX_VISUAL_PIXELS` (по умолчанию `32000000`) и общий `EXPORT_MAX_ARCHIVE_BYTES`. Превышение завершает задание без retry с `error_code: visual_too_large`.

## GEDCOM 7

`gedcom` строится только из активной части графа: персон, имён, parent-child relations, family unions и их участников. UUID становятся стабильными `INDI`/`FAM` cross-reference identifiers; имена, пол, deceased status, биография, заметки и privacy restriction сохраняются стандартными структурами. Связи представлены симметрично через `FAM.HUSB/WIFE/CHIL` и `INDI.FAMS/FAMC`; `PEDI` передаёт biological/adoptive/foster/guardian/step тип, а `STAT` — поддерживаемую стандартом оценку confidence.

Файл детерминирован, использует UTF-8 BOM, одинаковые CRLF line endings и `CONT` для многострочных значений. Soft-deleted записи, account memberships, S3 keys и media bytes не включаются. Союз из более чем двух участников раскладывается в несколько совместимых `FAM`, потому что GEDCOM 7 допускает в одной записи не больше двух partner pointers. Ограничение результата — общий `EXPORT_MAX_ARCHIVE_BYTES`; превышение завершает задание с `error_code: result_too_large`. Подробный mapping и известные ограничения описаны в [GEDCOM_EXPORT.md](GEDCOM_EXPORT.md).

`gedzip` использует тот же граф, добавляет стандартные `OBJE/FILE/FORM` records и взаимные ссылки персон на media. В архив попадают доступные originals и variants активных media из snapshot; каждый объект повторно проверяется по размеру и SHA-256. `gedcom.ged` ссылается только на безопасные ASCII пути `media/{mediaID}/...`, которые в точности совпадают с ZIP entry names. Подробности — в [GEDZIP_EXPORT.md](GEDZIP_EXPORT.md).

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
