# Media and private S3 API

Статус: реализованный контракт Этапов 8–9. Все API-пути имеют префикс `/api/v1`, требуют Bearer access token и ограничены активным membership указанного дерева.

PostgreSQL хранит метаданные и связи. Оригинальные байты находятся только в приватном S3 bucket. Клиент получает короткоживущие подписанные URL, но не постоянный публичный URL.

## Состояния и инварианты

- `pending` — upload intent создан, объект ещё не подтверждён;
- `uploaded` — `HEAD` подтвердил объект, а PostgreSQL-транзакция поставила обработку в очередь;
- `processing` — worker получил lease и проверяет фактические байты;
- `ready` — SHA-256, magic bytes и декодирование прошли, варианты изображения созданы;
- `rejected` — содержимое отсутствует, повреждено или не совпало с upload intent;
- `deleted` — метаданные мягко удалены, объект ожидает отложенной очистки.

Дополнительные правила:

- доступны `photo`, `document`, `other`;
- MVP принимает JPEG, PNG, WebP и PDF, по умолчанию не более 25 MiB;
- расширение файла должно соответствовать MIME;
- SHA-256 передаётся как 64 строчных hex-символа;
- объект получает случайный ключ `trees/{treeID}/media/{mediaID}/original`;
- одна медиа-запись может быть связана с несколькими персонами;
- `owner` и `editor` загружают и меняют файлы, `viewer` только читает;
- outsider получает `404` без раскрытия существования чужого объекта.

## 1. Создать upload intent

`POST /trees/{treeID}/media/upload-intents`

```json
{
  "client_request_id": "977874c3-a08b-4748-906d-94d3acee334b",
  "kind": "photo",
  "original_filename": "portrait.png",
  "mime_type": "image/png",
  "size_bytes": 48213,
  "checksum_sha256": "c091c7e8d3270b168fac308a4f176386b7d63e21f2bb25486280ce650b54b5b3"
}
```

Новый intent возвращает `201 Created`. Повтор с тем же `client_request_id` и теми же метаданными возвращает `200 OK`, тот же `media.id` и новый короткоживущий URL. Повтор с отличающимися метаданными возвращает `409 Conflict`.

```json
{
  "media": {
    "id": "85972d5b-8da5-487e-b030-e83196ce15aa",
    "status": "pending",
    "version": 1
  },
  "upload": {
    "url": "https://storage.example/...?X-Amz-Signature=...",
    "method": "PUT",
    "headers": {
      "Content-Type": ["image/png"],
      "X-Amz-Meta-Sha256": ["c091c7e8d3270b168fac308a4f176386b7d63e21f2bb25486280ce650b54b5b3"]
    },
    "expires_at": "2026-08-21T01:30:00Z"
  },
  "created": true,
  "access": {
    "role": "owner",
    "status": "active"
  }
}
```

Клиент обязан выполнить указанный метод и передать все `upload.headers` без изменений. Размер тела должен совпадать с `size_bytes`.

## 2. Подтвердить загрузку

`POST /trees/{treeID}/media/{mediaID}/complete`

Backend выполняет `HEAD` и сравнивает размер, MIME и SHA-256 metadata с intent. При совпадении одна PostgreSQL-транзакция переводит запись в `uploaded` и создаёт дедуплицированное задание `media.process`. На этом шаге `download` отсутствует: непроверенные байты клиенту не выдаются.

`complete` идемпотентен: повтор для `uploaded`, `processing` или `ready` возвращает текущее состояние без повторного изменения версии. Worker скачивает приватный объект, вычисляет SHA-256, проверяет magic bytes, полностью декодирует изображения и ограничивает число пикселей. Metadata от клиента сама по себе не считается доверенной.

Обычная последовательность версий нового файла: `pending/v1 → uploaded/v2 → processing/v3 → ready/v4` (или `rejected/v4`). Клиент всегда использует фактически полученную версию, а не предполагает номер состояния.

Для JPEG, PNG и WebP worker создаёт `thumbnail` (до 320 px) и `preview` (до 1600 px) в JPEG. Оригинал и варианты становятся доступными только после перехода в `ready`. PDF проходит проверку сигнатуры и checksum без создания графических вариантов.

## 3. Получить медиа или галерею

- `GET /trees/{treeID}/media/{mediaID}`;
- `GET /trees/{treeID}/media?kind=photo&status=ready&cursor=...&limit=20`.

Для `ready` файла ответ содержит короткоживущий `download` оригинала и массив `variants`; каждый вариант имеет собственные размеры, checksum и presigned inline-view URL, пригодный для `<img>`. Для остальных состояний постоянные и временные ссылки отсутствуют. Галерея использует cursor pagination, максимальный `limit` равен `100`.

## 4. Изменить описание

`PATCH /trees/{treeID}/media/{mediaID}`

```json
{
  "version": 4,
  "caption": "Семейный портрет",
  "description": "Снимок из домашнего архива"
}
```

Изменяются `caption` и `description`. Устаревшая версия возвращает `409 Conflict`.

## 5. Привязать к персоне

`POST /trees/{treeID}/persons/{personID}/media`

```json
{
  "media_id": "85972d5b-8da5-487e-b030-e83196ce15aa",
  "role": "profile",
  "sort_order": 0
}
```

Роли: `profile`, `gallery`, `document`, `other`. Привязать можно только проверенное `ready` media того же дерева. Повторная связь возвращает `409`.

Удаление связи:

`DELETE /trees/{treeID}/persons/{personID}/media/{mediaID}` → `204 No Content`.

Если файл был основной фотографией, ссылка очищается и версия персоны увеличивается.

## 6. Выбрать основную фотографию

`PUT /trees/{treeID}/persons/{personID}/primary-media`

```json
{
  "media_id": "85972d5b-8da5-487e-b030-e83196ce15aa",
  "person_version": 4
}
```

Media должна иметь `kind=photo`, быть доступной и уже связанной с персоной. Операция использует optimistic version персоны.

## 7. Удалить медиа

`DELETE /trees/{treeID}/media/{mediaID}`

```json
{
  "version": 5
}
```

Ответ: `204 No Content`. В одной PostgreSQL-транзакции запись становится `deleted`, очищаются ссылки `primary_media_id` и создаётся `media_asset.deleted` в `audit_log`. Физический объект остаётся приватным до безопасной очистки worker-ом после срока восстановления.

Просроченные `pending` сначала атомарно резервируются переводом в `deleted`, поэтому очистка не может удалить объект одновременно с успешным `complete`. `rejected` и давно удалённые записи очищаются повторяемо: повторный S3 DELETE безопасен, а метаданные удаляются только после успешной очистки объектов.

## Ошибки

- `400` — некорректный MIME, расширение, размер, checksum, UUID или cursor;
- `401` — отсутствует действительный access token;
- `403` — Viewer пытается изменить медиа;
- `404` — дерево, персона, media, attachment или S3 object отсутствуют либо недоступны;
- `409` — устаревшая версия, конфликт idempotency key, повторная связь или недопустимое состояние;
- `422` — загруженный объект не совпал с upload intent;
- `500/503` — обязательное S3-хранилище недоступно.

## Конфигурация Yandex Object Storage

Для production задаются `S3_ENDPOINT=https://storage.yandexcloud.net`, `S3_REGION=ru-central1`, приватный bucket и статический ключ сервисного аккаунта. `S3_USE_PATH_STYLE=true` поддерживает path-style URL, `S3_REQUEST_TIMEOUT` ограничивает сетевые операции. Для KMS указываются `S3_ENCRYPTION=aws:kms` и `S3_KMS_KEY_ID`.

Локальная разработка использует private MinIO из `compose.yaml`; API и worker при старте проверяют или создают bucket. Readiness API проверяет PostgreSQL и S3. Параметры lease/retry worker-а и сроки очистки перечислены в `.env.example` и подробнее описаны в `docs/WORKER.md`.
