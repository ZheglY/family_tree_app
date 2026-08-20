# Media and private S3 API

Статус: реализованный контракт Этапа 8. Все API-пути имеют префикс `/api/v1`, требуют Bearer access token и ограничены активным membership указанного дерева.

PostgreSQL хранит метаданные и связи. Оригинальные байты находятся только в приватном S3 bucket. Клиент получает короткоживущие подписанные URL, но не постоянный публичный URL.

## Состояния и инварианты

- `pending` — upload intent создан, объект ещё не подтверждён;
- `uploaded` — `HEAD` подтвердил объект, но worker ещё не проверил содержимое;
- `processing`, `ready`, `rejected` — состояния будущего worker-а;
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

Backend выполняет `HEAD` и сравнивает размер, MIME и SHA-256 metadata с intent. При совпадении состояние становится `uploaded`, версия — `2`, и ответ содержит подписанный `download`.

`complete` идемпотентен: повтор для `uploaded`, `processing` или `ready` возвращает текущее состояние без повторного изменения версии. Фактический SHA-256 байтов и magic bytes проверит worker до перехода в `ready`; metadata от клиента сама по себе не считается доверенной.

## 3. Получить медиа или галерею

- `GET /trees/{treeID}/media/{mediaID}`;
- `GET /trees/{treeID}/media?kind=photo&status=uploaded&cursor=...&limit=20`.

Для доступного файла ответ содержит короткоживущий `download` с URL, методом, обязательными headers и сроком действия. Галерея использует cursor pagination, максимальный `limit` равен `100`.

## 4. Изменить описание

`PATCH /trees/{treeID}/media/{mediaID}`

```json
{
  "version": 2,
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

Роли: `profile`, `gallery`, `document`, `other`. Привязать можно только `uploaded`, `processing` или `ready` media того же дерева. Повторная связь возвращает `409`.

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
  "version": 3
}
```

Ответ: `204 No Content`. В одной PostgreSQL-транзакции запись становится `deleted`, очищаются ссылки `primary_media_id` и создаётся `media_asset.deleted` в `audit_log`. Физический объект остаётся приватным до безопасной очистки worker-ом.

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

Локальная разработка использует private MinIO из `compose.yaml`; API при старте проверяет или создаёт bucket. Readiness проверяет и PostgreSQL, и S3.
