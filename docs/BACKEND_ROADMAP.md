# План самостоятельной разработки backend

Статус документа: техническая опорная спецификация. Backend реализует владелец проекта самостоятельно. Документ определяет рекомендуемые модули, сущности, API, порядок работы и критерии готовности.

## 1. Целевая архитектура

Для семейного домена подходит модульный монолит на Go. Аутентификация является единственным заранее принятым исключением и реализуется отдельным gRPC Identity Service согласно `docs/ADR-001-IDENTITY-SERVICE.md`:

```text
HTTP client
    -> middleware
    -> transport (HTTP, DTO, validation)
    -> gRPC client -> Identity Service (только auth-сценарии)
    -> service/use case (business rules and authorization)
    -> repository interfaces
    -> PostgreSQL / Yandex Object Storage
```

Текущий каркас уже использует `transport -> service -> repository` в `internal/features`. Этот принцип следует сохранить.

Рекомендуемые модули:

```text
internal/
  core/
    config/
    errors/
    logger/
    postgres/
    storage/
    transaction/
    validation/
    transport/http/
  features/
    users/
    trees/
    persons/
    relationships/
    events/
    places/
    media/
    exports/
    audit/
    health/
cmd/
  family_tree_app/
  worker/
migrations/
docs/
```

Внутри функционального модуля:

```text
feature/
  domain/       бизнес-сущности, enum и правила модуля
  repository/   интерфейс и postgres-реализация
  service/      сценарии приложения
  transport/    HTTP handler, request/response DTO, routes
```

Не следует превращать `core/domain` в склад всех бизнес-сущностей. Общий `core` содержит инфраструктуру и действительно общие value objects. Конкретной предметной моделью владеет соответствующий feature-модуль.

### Правило зависимостей

- transport знает service-интерфейс и DTO;
- service знает доменные типы и repository-интерфейсы;
- repository реализует интерфейс и знает PostgreSQL/S3;
- domain не импортирует HTTP, PostgreSQL или SDK хранилища;
- `main` создаёт зависимости и регистрирует маршруты;
- бизнес-транзакция начинается в service/use case, а не в handler.

### Что пока не нужно

- дополнительные микросервисы помимо Identity Service;
- графовая база;
- Kafka/RabbitMQ;
- event sourcing;
- универсальный generic repository;
- сложный DI-контейнер.

## 2. Общие технические решения

### Идентификаторы

Использовать UUID для публичных и доменных идентификаторов. Последовательные integer ID проще перебирать и сложнее безопасно переносить между архивами.

### Время

- техническое время хранить как `timestamptz` в UTC;
- в API возвращать RFC 3339;
- генеалогическая дата является отдельным value object и не равна `created_at`;
- локаль и часовой пояс дерева использовать только для представления.

### Мягкое удаление

Для деревьев, персон, отношений, союзов и медиа использовать `deleted_at`. Окончательную очистку выполнять отдельным процессом после срока восстановления.

### Конкурентное редактирование

У изменяемых агрегатов хранить `version`. Клиент отправляет известную версию, update выполняется с условием по версии. Если запись уже изменилась, API возвращает `409 Conflict`.

### Ошибки API

Единый контракт:

```json
{
  "error": {
    "code": "person_not_found",
    "message": "Person was not found",
    "details": {},
    "request_id": "uuid"
  }
}
```

Внутренние тексты БД и stack trace клиенту не возвращаются.

Основное отображение ошибок:

| Ситуация | HTTP |
|---|---:|
| Некорректный JSON/параметр | 400 |
| Не выполнен вход | 401 |
| Недостаточно прав | 403 |
| Ресурс отсутствует или недоступен | 404 |
| Конфликт/старая версия/дубликат | 409 |
| Семантическая ошибка данных | 422 |
| Превышен лимит | 429 |
| Неожиданная ошибка | 500 |

### Пагинация

Для обычных списков допустима cursor pagination. Ответ содержит `items` и `next_cursor`. Offset можно использовать в административных небольших списках, но не как единственный долгосрочный контракт.

## 3. Доменные сущности и таблицы

Ниже приведён целевой состав. Не все таблицы требуется создавать первой миграцией.

### 3.1. User

Учётная запись владельца данных. Источником истины для этой сущности является Identity Service; Family API использует выпущенный им UUID и при необходимости хранит только локальную проекцию несекретных полей.

Основные поля:

- `id uuid`;
- `email` — нормализованный, уникальный без учёта регистра;
- `display_name`;
- `status`: `pending`, `active`, `blocked`, `deleting`;
- `email_verified_at`;
- `created_at`, `updated_at`, `deleted_at`;
- `version`.

Пароль не хранить в этой таблице как часть публичной модели.

### 3.2. UserCredential

Хранится только в базе Identity Service.

- `user_id`;
- `password_hash`;
- `password_changed_at`;
- служебные поля для блокировки перебора при необходимости.

### 3.3. UserSession

Хранится только в базе Identity Service.

- `id`;
- `user_id`;
- `refresh_token_hash`;
- `user_agent`, `ip_address`;
- `expires_at`, `last_used_at`, `revoked_at`, `created_at`.

Refresh token хранится только в виде хеша. При обновлении выполняется ротация.

### 3.4. OneTimeToken

Для подтверждения email и восстановления пароля можно использовать одну техническую модель:

Таблица принадлежит Identity Service.

- `id`, `user_id`;
- `purpose`;
- `token_hash`;
- `expires_at`, `used_at`, `created_at`.

### 3.5. FamilyTree

- `id`;
- `name`, `description`;
- `owner_user_id`;
- `root_person_id nullable` — центральная персона по умолчанию;
- `cover_media_id nullable`;
- `privacy`: сначала только `private`;
- `locale`, `timezone`;
- `created_at`, `updated_at`, `deleted_at`, `version`.

### 3.6. TreeMember

- `tree_id`, `user_id`;
- `role`: `owner`, `editor`, `viewer`;
- `status`: `invited`, `active`, `revoked`;
- `invited_by`, `created_at`, `accepted_at`.

Ограничения:

- уникальная пара `(tree_id, user_id)`;
- ровно один активный owner;
- MVP автоматически создаёт owner при создании дерева.

### 3.7. Person

- `id`, `tree_id`;
- `sex`: `male`, `female`, `unknown`, `not_specified`;
- `life_status`: `alive`, `deceased`, `unknown`;
- `biography`, `notes`;
- `primary_media_id nullable`;
- `privacy_level`: на MVP `tree_members`;
- `created_by`, `updated_by`;
- `created_at`, `updated_at`, `deleted_at`, `version`.

Имя лучше вынести отдельно, чтобы поддержать фамилию при рождении, псевдонимы, транслитерации и исторические варианты.

### 3.8. PersonName

- `id`, `person_id`, `tree_id`;
- `type`: `primary`, `birth`, `married`, `alias`, `transliteration`, `other`;
- `given_name`, `patronymic`, `family_name`, `prefix`, `suffix`;
- `full_text` для поиска;
- `is_preferred`;
- `language_code`;
- `created_at`, `updated_at`.

У персоны должно быть ровно одно preferred-имя среди активных записей.

### 3.9. GenealogyDate

Логический value object, который в таблице события может быть представлен несколькими колонками:

- `qualifier`: `exact`, `about`, `before`, `after`, `between`, `unknown`;
- `date_from`;
- `date_to`;
- `display_text` — исходная запись пользователя, например «весна 1912»;
- `precision`: `day`, `month`, `year`, `text`.

Он нужен для неполных и приблизительных исторических дат.

### 3.10. ParentChildRelation

- `id`, `tree_id`;
- `parent_person_id`, `child_person_id`;
- `relation_type`: `biological`, `adoptive`, `foster`, `guardian`, `step`, `unknown`;
- `confidence`: `unverified`, `probable`, `confirmed`, `disputed`;
- `note`;
- `created_by`, `created_at`, `updated_at`, `deleted_at`, `version`.

Обязательные проверки:

- parent и child различны;
- обе персоны принадлежат дереву связи;
- нет активного дубликата одинаковой связи;
- добавление не создаёт направленный цикл.

### 3.11. FamilyUnion и UnionMember

`FamilyUnion`:

- `id`, `tree_id`;
- `type`: `marriage`, `civil_union`, `partnership`, `engagement`, `unknown`;
- даты начала и окончания как GenealogyDate;
- `start_place_id`, `end_place_id`;
- `end_reason`, `note`;
- audit-поля, `deleted_at`, `version`.

`UnionMember`:

- `union_id`, `person_id`, `tree_id`;
- `role` — необязательное расширяемое поле;
- `created_at`;
- уникальная пара `(union_id, person_id)`.

Союз — отдельная сущность, потому что у него есть даты, место, документы и собственная история.

### 3.12. PersonEvent

- `id`, `tree_id`, `person_id`;
- `type`: `birth`, `death`, `burial`, `baptism`, `residence`, `education`, `occupation`, `military_service`, `award`, `custom`;
- GenealogyDate;
- `place_id nullable`;
- `title`, `description`;
- `confidence`;
- audit-поля, `deleted_at`, `version`.

Рождение и смерть лучше хранить как события, а не дублировать несколькими независимыми источниками истины в Person. Для быстрых выборок позднее допустима производная денормализация.

### 3.13. Place

- `id`, `tree_id nullable`;
- `name`, `normalized_name`;
- `country_code`, `region`, `district`, `locality`;
- `latitude`, `longitude nullable`;
- `parent_place_id nullable`;
- `created_at`, `updated_at`.

В MVP достаточно ручного текста места или небольшой нормализованной модели. Геокодинг не должен блокировать основную разработку.

### 3.14. MediaAsset

- `id`, `tree_id`;
- `kind`: `photo`, `document`, `other`;
- `status`: `pending`, `uploaded`, `processing`, `ready`, `rejected`, `deleted`;
- `object_key` — случайный внутренний ключ;
- `original_filename`;
- `mime_type`, `size_bytes`, `checksum_sha256`;
- `width`, `height nullable`;
- `caption`, `description`;
- GenealogyDate для даты материала при необходимости;
- `uploaded_by`, `created_at`, `updated_at`, `deleted_at`, `version`.

Производные файлы можно хранить в `MediaVariant`:

- `media_id`, `variant`: `thumbnail`, `card`, `preview`;
- `object_key`, `mime_type`, `size_bytes`, `width`, `height`.

### 3.15. Связи медиа

Не помещать один `person_id` в MediaAsset. Использовать связующие таблицы:

- `PersonMedia(person_id, media_id, role, sort_order)`;
- `EventMedia(event_id, media_id, role, sort_order)`;
- `UnionMedia(union_id, media_id, role, sort_order)`.

Так одна семейная фотография связывается с несколькими людьми.

### 3.16. Source и Citation — после MVP

`Source` описывает документ, архив или рассказ человека. `Citation` связывает источник с событием, отношением или другим утверждением. Архитектурное место для них следует учитывать, но интерфейс доказательств можно отложить.

### 3.17. ExportJob

- `id`, `tree_id`, `requested_by`;
- `format`: `json_backup`, `zip_backup`, `pdf`, `png`, `svg`, `gedcom`, `gedzip`;
- `parameters jsonb`;
- `status`: `queued`, `running`, `completed`, `failed`, `expired`;
- `progress`, `result_object_key`, `error_code`;
- `created_at`, `started_at`, `finished_at`, `expires_at`.

### 3.18. AuditLog

- `id`, `tree_id nullable`, `actor_user_id nullable`;
- `action`, `entity_type`, `entity_id`;
- `request_id`, `ip_address`;
- безопасный `changes jsonb` без паролей и токенов;
- `created_at`.

## 4. Связи агрегатов

```text
User --< TreeMember >-- FamilyTree
FamilyTree --< Person --< PersonName
Person(parent) --< ParentChildRelation >-- Person(child)
FamilyTree --< FamilyUnion --< UnionMember >-- Person
Person --< PersonEvent >-- Place
FamilyTree --< MediaAsset --< MediaVariant
Person --< PersonMedia >-- MediaAsset
PersonEvent --< EventMedia >-- MediaAsset
FamilyUnion --< UnionMedia >-- MediaAsset
FamilyTree --< ExportJob
User --< UserSession
```

`tree_id` намеренно присутствует во многих дочерних таблицах. Это упрощает авторизационные запросы, индексацию и проверку tenant isolation. Его согласованность необходимо защищать ограничениями и сервисными правилами.

## 5. Авторизация

Каждый use case над семейными данными выполняет цепочку:

```text
authenticated user
  -> active TreeMember for tree_id
  -> role permits operation
  -> target entity belongs to the same tree
  -> business invariant permits operation
```

Матрица прав:

| Операция | Owner | Editor | Viewer |
|---|:---:|:---:|:---:|
| Просматривать дерево | + | + | + |
| Редактировать данные | + | + | - |
| Загружать файлы | + | + | - |
| Экспортировать | + | настраиваемо | настраиваемо |
| Управлять участниками | + | - | - |
| Удалять дерево | + | - | - |
| Передавать владение | + | - | - |

В MVP реально используется owner, но проверки проектируются через TreeMember, чтобы позднее не переписывать все запросы.

Для скрытия существования чужого объекта API обычно возвращает `404`, а не `403`, если пользователь вообще не состоит в дереве.

## 6. Аутентификация

Источником истины для аккаунтов, credentials, одноразовых токенов и сессий является отдельный Identity Service. Family API публикует браузерные HTTP endpoints и вызывает версионированный gRPC-контракт `identity.v1`. Access token проверяется Family API локально; refresh, logout и управление сессиями выполняются через Identity Service.

Рекомендуемая схема для браузерного клиента:

- короткоживущий access token;
- refresh token в `HttpOnly + Secure + SameSite` cookie;
- хеш refresh token в `UserSession`;
- ротация refresh token при каждом обновлении;
- отзыв сессии при повторном использовании старого токена;
- CSRF-защита для cookie-based операций;
- пароль хешируется Argon2id или bcrypt с актуальными параметрами;
- токены email/reset одноразовые, короткоживущие и хранятся в виде хеша.

Если frontend и backend всегда работают на одном origin, серверная opaque-session cookie является ещё более простой альтернативой. Выбор нужно зафиксировать ADR до реализации auth и не смешивать две схемы без причины.

## 7. Каталог API `/api/v1`

Ниже — целевой каталог. `Auth` означает обязательную аутентификацию, роль применяется к указанному дереву.

### 7.1. Health

| Метод и путь | Доступ | Назначение |
|---|---|---|
| `GET /health/live` | public | Процесс работает |
| `GET /health/ready` | internal/public | PostgreSQL и обязательные зависимости готовы |

### 7.2. Auth

| Метод и путь | Назначение |
|---|---|
| `POST /auth/register` | Создать pending-user и отправить подтверждение |
| `POST /auth/verify-email` | Активировать email одноразовым токеном |
| `POST /auth/resend-verification` | Повторно отправить письмо с rate limit |
| `POST /auth/login` | Создать сессию |
| `POST /auth/refresh` | Ротировать refresh token и выдать access token |
| `POST /auth/logout` | Отозвать текущую сессию |
| `POST /auth/logout-all` | Отозвать все сессии пользователя |
| `POST /auth/forgot-password` | Создать запрос восстановления; ответ не раскрывает наличие email |
| `POST /auth/reset-password` | Проверить токен, изменить пароль, отозвать старые сессии |

### 7.3. Текущий пользователь

| Метод и путь | Назначение |
|---|---|
| `GET /users/me` | Получить профиль |
| `PATCH /users/me` | Изменить разрешённые поля профиля |
| `GET /users/me/sessions` | Список активных сессий |
| `DELETE /users/me/sessions/{sessionID}` | Отозвать конкретную сессию |
| `POST /users/me/change-password` | Изменить пароль после проверки старого |
| `DELETE /users/me` | Запустить безопасное удаление аккаунта |

### 7.4. Деревья

| Метод и путь | Роль | Назначение |
|---|---|---|
| `POST /trees` | Auth | Создать дерево и owner membership |
| `GET /trees` | Auth | Доступные пользователю деревья |
| `GET /trees/{treeID}` | Viewer+ | Получить дерево и права пользователя |
| `PATCH /trees/{treeID}` | Owner | Настройки и optimistic version |
| `DELETE /trees/{treeID}` | Owner | Мягко удалить дерево |
| `POST /trees/{treeID}/restore` | Owner | Восстановить в разрешённый срок |
| `PUT /trees/{treeID}/root-person` | Owner/Editor | Выбрать центральную персону |

### 7.5. Участники — подготовить модель, UI можно после MVP

| Метод и путь | Роль | Назначение |
|---|---|---|
| `GET /trees/{treeID}/members` | Owner | Список участников |
| `POST /trees/{treeID}/invitations` | Owner | Пригласить по email |
| `POST /invitations/{token}/accept` | Auth | Принять приглашение |
| `PATCH /trees/{treeID}/members/{userID}` | Owner | Изменить роль |
| `DELETE /trees/{treeID}/members/{userID}` | Owner | Отозвать доступ |

### 7.6. Персоны

| Метод и путь | Роль | Назначение |
|---|---|---|
| `POST /trees/{treeID}/persons` | Editor+ | Создать персону и preferred-имя |
| `GET /trees/{treeID}/persons` | Viewer+ | Поиск, фильтры и пагинация |
| `GET /trees/{treeID}/persons/{personID}` | Viewer+ | Полная карточка |
| `PATCH /trees/{treeID}/persons/{personID}` | Editor+ | Изменить карточку с version |
| `DELETE /trees/{treeID}/persons/{personID}` | Editor+ | Мягкое удаление после preview зависимостей |
| `POST /trees/{treeID}/persons/{personID}/restore` | Editor+ | Восстановить персону |
| `GET /trees/{treeID}/persons/{personID}/deletion-impact` | Editor+ | Показать, какие связи затронет удаление |
| `POST /trees/{treeID}/persons/{personID}/names` | Editor+ | Добавить альтернативное имя |
| `PATCH /trees/{treeID}/persons/{personID}/names/{nameID}` | Editor+ | Изменить имя |
| `DELETE /trees/{treeID}/persons/{personID}/names/{nameID}` | Editor+ | Удалить непредпочтительное имя |
| `PUT /trees/{treeID}/persons/{personID}/primary-media` | Editor+ | Выбрать основное фото |

В `GET /persons` предусмотреть параметры `query`, `life_status`, `has_media`, `born_from`, `born_to`, `cursor`, `limit`.

### 7.7. Родительские связи

| Метод и путь | Роль | Назначение |
|---|---|---|
| `POST /trees/{treeID}/parent-child-relations` | Editor+ | Создать связь после проверки цикла |
| `GET /trees/{treeID}/parent-child-relations/{relationID}` | Viewer+ | Получить связь |
| `PATCH /trees/{treeID}/parent-child-relations/{relationID}` | Editor+ | Изменить тип/достоверность/заметку |
| `DELETE /trees/{treeID}/parent-child-relations/{relationID}` | Editor+ | Мягко удалить ошибочную связь |

Создание родственника «в один шаг» на UI всё равно может вызывать две операции в одной транзакции через удобный endpoint позднее. Сначала полезнее сделать простые и проверяемые person + relation use cases.

### 7.8. Семейные союзы

| Метод и путь | Роль | Назначение |
|---|---|---|
| `POST /trees/{treeID}/unions` | Editor+ | Создать союз с участниками |
| `GET /trees/{treeID}/unions/{unionID}` | Viewer+ | Получить союз |
| `PATCH /trees/{treeID}/unions/{unionID}` | Editor+ | Изменить союз |
| `DELETE /trees/{treeID}/unions/{unionID}` | Editor+ | Мягко удалить союз |
| `POST /trees/{treeID}/unions/{unionID}/members` | Editor+ | Добавить участника |
| `DELETE /trees/{treeID}/unions/{unionID}/members/{personID}` | Editor+ | Удалить участника |

### 7.9. События

| Метод и путь | Роль | Назначение |
|---|---|---|
| `POST /trees/{treeID}/persons/{personID}/events` | Editor+ | Создать событие |
| `GET /trees/{treeID}/persons/{personID}/events` | Viewer+ | События персоны |
| `GET /trees/{treeID}/events/{eventID}` | Viewer+ | Получить событие |
| `PATCH /trees/{treeID}/events/{eventID}` | Editor+ | Изменить событие |
| `DELETE /trees/{treeID}/events/{eventID}` | Editor+ | Мягко удалить событие |

### 7.10. Граф

| Метод и путь | Роль | Назначение |
|---|---|---|
| `GET /trees/{treeID}/graph` | Viewer+ | Получить фрагмент графа |
| `GET /trees/{treeID}/persons/{personID}/kinship/{otherPersonID}` | Viewer+ | Вычислить путь родства, после MVP |

Параметры `/graph`:

- `center_person_id`;
- `ancestors_depth`;
- `descendants_depth`;
- `include_partners`;
- разумный серверный максимум глубины и количества узлов.

Ответ содержит отдельные массивы `persons`, `parent_child_relations`, `unions` и `union_members`. Не следует возвращать рекурсивно вложенных персон: плоская графовая форма проще и не дублирует узлы.

### 7.11. Медиа

| Метод и путь | Роль | Назначение |
|---|---|---|
| `POST /trees/{treeID}/media/upload-intents` | Editor+ | Проверить лимиты и выдать presigned upload URL |
| `POST /trees/{treeID}/media/{mediaID}/complete` | Editor+ | Подтвердить загрузку и поставить обработку в очередь |
| `GET /trees/{treeID}/media` | Viewer+ | Галерея и фильтры |
| `GET /trees/{treeID}/media/{mediaID}` | Viewer+ | Метаданные и временные URL вариантов |
| `PATCH /trees/{treeID}/media/{mediaID}` | Editor+ | Подпись, описание и дата |
| `DELETE /trees/{treeID}/media/{mediaID}` | Editor+ | Мягко удалить метаданные и запланировать очистку |
| `POST /trees/{treeID}/persons/{personID}/media` | Editor+ | Привязать готовый файл к персоне |
| `DELETE /trees/{treeID}/persons/{personID}/media/{mediaID}` | Editor+ | Удалить привязку |
| `POST /trees/{treeID}/events/{eventID}/media` | Editor+ | Привязать к событию |
| `DELETE /trees/{treeID}/events/{eventID}/media/{mediaID}` | Editor+ | Удалить привязку |

Frontend никогда не получает постоянный публичный URL приватного объекта.

### 7.12. Экспорт

| Метод и путь | Роль | Назначение |
|---|---|---|
| `POST /trees/{treeID}/exports` | разрешённая роль | Создать фоновое задание |
| `GET /trees/{treeID}/exports` | разрешённая роль | История экспортов пользователя |
| `GET /trees/{treeID}/exports/{exportID}` | requester/Owner | Статус и прогресс |
| `GET /trees/{treeID}/exports/{exportID}/download` | requester/Owner | Короткоживущая ссылка на результат |
| `DELETE /trees/{treeID}/exports/{exportID}` | requester/Owner | Удалить результат |

## 8. Yandex Object Storage

Bucket должен быть приватным. PostgreSQL хранит `object_key`, но не тело файла.

Поток загрузки:

1. Client отправляет имя, размер, MIME и checksum в `upload-intents`.
2. Service проверяет TreeMember, расширение, MIME, размер и квоту.
3. Создаёт MediaAsset со статусом `pending` и случайным object key.
4. Возвращает короткоживущий presigned PUT/POST URL.
5. Client загружает объект напрямую в S3.
6. Client вызывает `complete`.
7. Backend делает HEAD, проверяет фактический размер/метаданные.
8. Media получает `uploaded`, worker проверяет файл и создаёт варианты.
9. После обработки статус становится `ready` или `rejected`.

Дополнительно:

- KMS server-side encryption;
- CORS только для нужных origin и методов;
- multipart upload для крупных файлов позднее;
- lifecycle для незавершённых загрузок и временных экспортов;
- случайный путь вида `trees/{treeID}/media/{mediaID}/original`;
- проверка magic bytes, декодирование изображения и антивирус;
- никакого доверия к `Content-Type` от клиента;
- периодическая очистка `pending`, orphaned и soft-deleted объектов.

## 9. Порядок самостоятельной реализации

Каждый этап заканчивается работающим вертикальным срезом. Не создавать все таблицы и пустые интерфейсы заранее.

### Этап 0. Стабилизировать HTTP-каркас

Реализовать:

- исправный `ResponseWriter.WriteHeader`;
- единый JSON error envelope;
- `Content-Type: application/json` до `WriteHeader`;
- middleware в порядке request ID -> logger -> recovery -> trace;
- корректный возврат после отправки ошибки handler-ом;
- ограничение размера body;
- JSON decoder с запретом неизвестных полей и лишних JSON-значений;
- конфигурацию таймаутов HTTP server;
- `/health/live`.

Проверить unit-тестами middleware и response handler.

Готово, когда `go test ./...` проходит и panic даёт один контролируемый ответ 500.

### Этап 1. PostgreSQL

Реализовать:

- конфигурацию DSN без логирования секрета;
- `pgxpool`;
- ping при старте/readiness;
- graceful close;
- миграции up/down;
- helper для транзакций;
- integration test database через контейнер или отдельную тестовую БД.

Первая миграция может содержать только auth-таблицы, а не всю будущую схему.

Готово, когда чистая БД разворачивается одной командой, миграции повторяемы, readiness отражает недоступность PostgreSQL.

### Этап 2. Регистрация в Identity Service

Вертикальный срез:

1. Domain: User, email value object, статусы.
2. Migration: users, credentials, one_time_tokens.
3. Repository: create/find by normalized email/verify.
4. Service: register и verify email.
5. Transport: DTO, validation, routes.
6. Тесты: duplicate email, weak password, expired/used token.

На первом локальном этапе отправку email можно заменить интерфейсом Mailer с dev-реализацией, которая безопасно показывает ссылку только в development.

### Этап 3. Вход, сессии и интеграция сервисов

Реализовать login, refresh rotation, logout и logout-all в Identity Service. В Family API добавить gRPC client, HTTP auth endpoints, локальную проверку access token, auth middleware и `GET /users/me`.

Тесты обязательно покрывают:

- неверный пароль без раскрытия деталей;
- истёкшую/отозванную сессию;
- повторное использование refresh token;
- смену пароля с отзывом сессий;
- rate limit.

### Этап 4. FamilyTree и tenant isolation

Статус: завершён 20 августа 2026 года.

Вертикальный срез:

- FamilyTree и TreeMember;
- создание дерева в одной транзакции с owner membership;
- список доступных деревьев;
- получение/изменение/мягкое удаление;
- middleware или service helper загрузки membership;
- аудит удаления.

Самый важный интеграционный тест: User A не может читать или менять дерево User B даже при знании UUID.

### Этап 5. Person

Статус: завершён 20 августа 2026 года.

Начать с минимальной Person и PersonName:

- создать;
- получить;
- изменить с version;
- список/поиск;
- мягко удалить и восстановить.

После этого добавить GenealogyDate и события birth/death. Не перегружать первый endpoint всеми будущими полями.

### Этап 6. Родительский граф

Статус: завершён 20 августа 2026 года.

Реализовать ParentChildRelation и проверку циклов.

Алгоритм перед вставкой `parent -> child`: проверить, существует ли уже путь `child -> ... -> parent`. Для PostgreSQL подойдёт recursive CTE; в service можно дополнительно ограничить число посещённых узлов.

Тесты:

- self relation;
- duplicate;
- прямая и длинная циклическая цепочка;
- связь персон разных деревьев;
- soft-deleted relation;
- одновременные конкурирующие вставки внутри транзакции.

Затем реализовать `/graph` с ограничением глубины и количества узлов.

### Этап 7. Семейные союзы

Статус: завершён 20 августа 2026 года.

Создать FamilyUnion и UnionMember. Создание союза с первыми участниками выполнять транзакционно. Добавить даты и места после базового CRUD.

### Этап 8. Media и S3

Статус: завершён 21 августа 2026 года.

Сначала сделать адаптер хранилища и интеграционные тесты против S3-compatible development storage либо отдельного тестового bucket. Затем upload intent, complete, привязки и удаление.

Не пропускать промежуточные статусы: БД и S3 не поддерживают общую ACID-транзакцию, поэтому процесс должен быть восстанавливаемым и идемпотентным.

### Этап 9. Worker

Статус: завершён 21 августа 2026 года.

Для первой версии использовать PostgreSQL-backed job queue:

- job claim через `FOR UPDATE SKIP LOCKED`;
- lease/heartbeat;
- retry count и backoff;
- идемпотентный handler;
- dead/failed state;
- graceful shutdown.

Первые задания: миниатюры, очистка незавершённых загрузок, экспорт.

### Этап 10. Export

Статус: завершён 22 августа 2026 года. Реализованы versioned JSON manifest, ZIP backup с файлами/checksums, проверяемый offline restore, визуальный PDF/PNG/SVG, GEDCOM 7 и GEDZIP 7. Backup, visual, GEDCOM и GEDZIP pipelines проверяются на чистой PostgreSQL schema и реальном S3-compatible MinIO.

Порядок форматов:

1. JSON manifest без файлов;
2. ZIP backup с файлами и checksums;
3. визуальный PDF/PNG/SVG;
4. GEDCOM 7;
5. GEDZIP.

JSON schema должна иметь собственную версию. Технический offline restore резервной копии готов и проверен на чистой БД. Публичный пользовательский import не добавлять без отдельного mapping/conflict-preview контракта и tenant-scoped авторизации.

### Этап 11. Hardening и release

Статус: в процессе с 22 августа 2026 года. Первый срез добавил OpenAPI 3.1.1 для всей зарегистрированной HTTP surface, автоматическую проверку route/security drift и GitHub Actions quality gates с `gofmt`, `go vet`, race detector, clean PostgreSQL и real S3-compatible MinIO integration suite. Второй срез добавил общие security headers, exact allowlist credentialed CORS, validated preflight и CSRF origin/Referer/Fetch-Metadata verification для state-changing requests. Третий срез заменил общие success JSON objects типизированными схемами всех transport DTO и запретил их возврат contract-тестом. Четвёртый срез добавил отдельные Prometheus listeners, bounded-cardinality HTTP/PostgreSQL/job queue/worker metrics и JSON logging без raw URL, query, auth headers, bodies и пользовательских 4xx error values. Пятый срез добавил quiesced PostgreSQL custom dump и private S3 backup/restore drill с manifests, SHA-256/metadata verification, пустыми targets и выполнением на каждом CI run. Шестой срез закрепил CI budget для 12 конкурентных graph reads в дереве на 10 000 персон и расширил cross-service E2E проверками API/worker metrics и label privacy.

- OpenAPI как проверяемый контракт;
- unit, repository integration и API end-to-end тесты;
- race detector;
- линтеры и статический анализ;
- security headers, CORS, CSRF, rate limits;
- метрики HTTP/DB/job queue;
- structured logging без PII и secrets;
- резервные копии PostgreSQL и bucket;
- регулярный restore drill;
- нагрузочный тест большого графа;
- CI/CD и staging.

## 10. Шаблон реализации каждого use case

Для любой новой операции следовать одному порядку:

1. Описать пользовательский сценарий и права.
2. Выписать инварианты и возможные доменные ошибки.
3. Определить request/response независимо от таблиц.
4. Добавить минимальную миграцию и индексы.
5. Определить repository-интерфейс на языке use case, а не CRUD ради CRUD.
6. Реализовать PostgreSQL repository.
7. Реализовать service с авторизацией и транзакцией.
8. Реализовать transport: parse -> validate -> call service -> map response/error.
9. Добавить unit-тесты service и integration-тесты repository/API.
10. Проверить OpenAPI и критерии готовности.

Пример хорошего repository-метода: `CreateTreeWithOwner`, если атомарность является обязанностью persistence-слоя через переданную транзакцию. Пример слишком общего интерфейса: один глобальный `Repository[T]`, скрывающий важные запросы и ограничения.

## 11. Индексы, которые следует планировать

- уникальный нормализованный email активного User;
- `tree_members(user_id, status)` и unique `(tree_id, user_id)`;
- `persons(tree_id, deleted_at)`;
- полнотекстовый/trigram индекс PersonName в пределах `tree_id`;
- `parent_child_relations(tree_id, parent_person_id)`;
- `parent_child_relations(tree_id, child_person_id)`;
- unique active relation для parent/child/type согласно принятому правилу;
- `union_members(tree_id, person_id)`;
- `person_events(tree_id, person_id, type)`;
- `media_assets(tree_id, status, created_at)`;
- `export_jobs(status, created_at)` для worker;
- `audit_log(tree_id, created_at)`.

Индекс добавляется под конкретный запрос и проверяется `EXPLAIN`, а не только «на всякий случай».

## 12. Стратегия тестирования

### Unit

- value objects;
- business validation;
- service use cases с fake/mock repository;
- error mapping;
- графовые проверки, где они реализованы вне БД.

### Integration

- реальные миграции PostgreSQL;
- repository queries;
- транзакции и unique constraints;
- tenant isolation;
- recursive CTE;
- S3 adapter;
- worker claim/retry.

### End-to-end API

- register -> verify -> login;
- create tree -> create persons -> connect -> read graph;
- User B не видит дерево User A;
- upload intent -> complete -> attach -> download;
- soft delete -> restore;
- export -> worker -> download.

## 13. Definition of Done для backend MVP

- все MVP endpoints документированы в OpenAPI;
- чистое окружение поднимается миграциями;
- два пользователя полностью изолированы;
- поддержаны минимум три поколения, повторные союзы и типы parent-child;
- циклы родительства не создаются;
- большие графовые ответы ограничены;
- файлы приватны и доступны только через короткоживущие ссылки;
- удалённые записи восстанавливаются в установленный срок;
- тяжёлые экспорты не блокируют HTTP;
- auth, authorization и основные пользовательские цепочки покрыты тестами;
- секреты и персональные документы не попадают в логи;
- backup восстановлен в тестовом окружении, а не только создан;
- `go test ./...` и статические проверки проходят в CI.

## 14. Текущий статус реализации

Статус на 21 августа 2026 года:

- Этап 0 завершён: HTTP-фундамент стабилизирован и покрыт unit-тестами;
- в Identity Service реализованы PostgreSQL, миграции, регистрация и подтверждение email;
- в Identity Service реализованы login, refresh rotation с обнаружением replay, logout и logout-all;
- Family API содержит gRPC client с deadline и request ID, публичные HTTP auth endpoints, защищённую refresh-cookie и локальную проверку Ed25519 access token;
- auth-срез проверяется unit-, repository-, gRPC- и PostgreSQL integration-тестами.

Family API также реализует `GET /users/me`, список активных сессий и отзыв выбранной сессии с обязательной проверкой владельца в Identity Service.

Identity Service и Family API реализуют смену пароля после проверки текущего пароля и восстановление через одноразовый часовой токен. Оба сценария атомарно отзывают серверные сессии; HTTP-ответ восстановления не раскрывает наличие email.

Family API ограничивает публичные auth-попытки по IP и HMAC-обезличенному аккаунту. Для одного development-процесса доступен ограниченный in-memory backend, для нескольких экземпляров — атомарный Redis backend.

Этап 4 завершён: Family API подключён к отдельной PostgreSQL, получил встроенные миграции, `FamilyTree`, `TreeMember`, optimistic version, мягкое удаление/восстановление и аудит жизненного цикла. Все запросы деревьев ограничены активным membership; интеграционный PostgreSQL-тест доказывает, что пользователь не может прочитать, изменить или удалить чужое дерево даже при знании UUID. Добавлены отдельные liveness и readiness проверки.

До полного production-hardening Этапа 3 остаются production mailer и аутентифицированный gRPC transport. Family API не использует базу Identity Service.

Этап 5 завершён: добавлены `Person` и обязательное preferred `PersonName`, транзакционный CRUD агрегата, cursor pagination и поиск по имени, optimistic version, мягкое удаление/восстановление и audit log. Составной внешний ключ защищает принадлежность имени дереву; PostgreSQL integration-тесты покрывают outsider/viewer роли и запрещают cross-tree запись.

Этап 6 завершён: реализованы типизированные `ParentChildRelation`, optimistic version, мягкое удаление и аудит. Recursive CTE проверяет прямые и длинные циклы, а advisory lock сериализует конкурентные изменения одного дерева. Ограниченный `/graph` возвращает плоские массивы персон и связей в repeatable-read транзакции; PostgreSQL-тесты покрывают tenant isolation, роли, дубликаты, soft-delete и встречные конкурентные вставки.

Этап 7 завершён: добавлены `FamilyUnion` и `UnionMember`, транзакционное создание союза с первыми участниками, optimistic version, управление составом, мягкое удаление с аудитом и tenant-scoped права. `/graph` по `include_partners=true` включает активные союзы и партнёров одним переходом; cross-tree участники блокируются составными внешними ключами и repository-проверками.

Этап 8 завершён: добавлены `MediaAsset` и `PersonMedia`, private S3 adapter для Yandex Object Storage и MinIO, presigned PUT/GET, идемпотентный upload intent, проверяемый через HEAD complete, gallery CRUD, привязки к персоне и выбор основной фотографии. PostgreSQL хранит только метаданные и случайный object key; удаление мягкое, очищает primary media и оставляет физическую очистку восстанавливаемому worker-у.

Этап 9 завершён: отдельный `cmd/worker` использует PostgreSQL-backed очередь с `FOR UPDATE SKIP LOCKED`, lease/heartbeat, exponential retry, `dead` state и идемпотентными handlers. `media.process` проверяет фактический SHA-256, magic bytes и декодирование, создаёт thumbnail/preview и только затем открывает скачивание и привязки. `media.cleanup` безопасно резервирует просроченные pending и после retention удаляет originals/variants и metadata. Интеграционные тесты покрывают конкурирующий claim, потерю lease, retry/dead и реальный MinIO pipeline.

Этап 10 завершён: реализованы `json_backup`, `zip_backup` schema v1, offline restore, визуальные `pdf`/`png`/`svg`, `gedcom` и `gedzip`. Создание export и job атомарно, worker формирует repeatable-read snapshot, сохраняет checksum и приватный S3 object, а API предоставляет tenant-scoped историю и короткоживущую ссылку только requester/Owner. ZIP backup содержит manifest, checksums и проверенные оригиналы/варианты `uploaded`/`processing`/`ready` media без раскрытия S3 keys. Restore валидирует schema, canonical paths, checksum set и связи, сохраняет UUID, транзакционно восстанавливает граф и компенсирует S3 uploads при ошибке. Визуальные форматы используют общий детерминированный generations layout, выравнивают партнёров и исключают soft-deleted записи; PDF встраивает Unicode font. GEDCOM 7 экспортирует активный граф со стабильными `INDI`/`FAM` identifiers, взаимными family pointers, типом родительства и безопасным многострочным UTF-8. GEDZIP дополняет его стандартными `OBJE/FILE/FORM`, проверенными S3 media и точным соответствием локальных путей ZIP entries. Clean PostgreSQL + real MinIO integration-тесты подтверждают restore, visual, GEDCOM и GEDZIP pipelines. Результат экспорта имеет TTL, ручное удаление и периодическую очистку; audit фиксирует создание, скачивание, удаление и восстановление.

Этап 11 в процессе: проверяемая OpenAPI surface с типизированными success responses, CI quality gates, browser security perimeter, observability, PostgreSQL/S3 backup drills и large-graph/cross-service E2E gates завершены. Остался staging release flow. Production mailer и service-to-service аутентификацию завершить до публичного релиза.
