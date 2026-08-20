# Family trees HTTP API

Статус: реализованный контракт Этапа 4. Все пути ниже имеют префикс `/api/v1` и требуют `Authorization: Bearer <access_token>`.

## Модель доступа

Каждое чтение выполняется через активную запись `tree_members`. Пользователь без membership получает `404 Not Found`, даже если знает UUID дерева. Это не раскрывает существование чужих семейных данных.

В MVP дерево создаётся с одним активным участником роли `owner`. Читать дерево могут `owner`, `editor` и `viewer`; менять настройки, удалять и восстанавливать — только `owner`. API управления участниками остаётся отдельным будущим срезом.

Семейные данные приватны по умолчанию. `owner_user_id` и `TreeMember` — UUID аккаунтов из Identity Service; Family API не хранит пароли или сессии в своей PostgreSQL.

## Создать дерево

`POST /trees`

```json
{
  "name": "Род Волконских",
  "description": "Семейный архив",
  "locale": "ru-RU",
  "timezone": "Europe/Moscow"
}
```

`name` обязателен. Если `locale` и `timezone` не заданы, используются `ru-RU` и `UTC`. Дерево и owner membership создаются атомарно. Ответ: `201 Created`.

## Получить доступные деревья

`GET /trees`

```json
{
  "items": [
    {
      "tree": {
        "id": "8f2850bb-b804-4e01-a188-939cb5d094a1",
        "name": "Род Волконских",
        "description": "Семейный архив",
        "owner_user_id": "c301485d-fc99-4931-8652-f1ff7bfffc5a",
        "privacy": "private",
        "locale": "ru-RU",
        "timezone": "Europe/Moscow",
        "created_at": "2026-08-20T12:00:00Z",
        "updated_at": "2026-08-20T12:00:00Z",
        "version": 1
      },
      "access": {
        "role": "owner",
        "status": "active"
      }
    }
  ]
}
```

Мягко удалённые деревья в список не входят.

## Получить дерево

`GET /trees/{treeID}` возвращает тот же объект `tree` и текущий `access`. Ответ доступен только активному участнику.

## Изменить настройки

`PATCH /trees/{treeID}`

```json
{
  "version": 1,
  "name": "Обновлённый род Волконских",
  "description": "Архив и семейные воспоминания"
}
```

Изменяемые поля: `name`, `description`, `locale`, `timezone`. Нужно передать хотя бы одно поле и текущую положительную `version`. При успешном изменении версия увеличивается. Устаревшая версия возвращает `409 Conflict`.

## Удалить и восстановить

`DELETE /trees/{treeID}` и `POST /trees/{treeID}/restore` принимают тело с текущей версией:

```json
{
  "version": 2
}
```

Удаление мягкое: запись и membership сохраняются, обычные чтения скрывают дерево, версия увеличивается. Восстановление также увеличивает версию. Обе операции выполняются транзакционно вместе с записью `audit_log`, содержащей actor, request ID, IP-адрес и изменение версии.

## Ошибки

API использует общий JSON envelope:

```json
{
  "error": {
    "code": "not_found",
    "message": "Family tree was not found",
    "details": {},
    "request_id": "request-id"
  }
}
```

- `400` — некорректный JSON или доменные значения;
- `401` — отсутствует или недействителен access token;
- `403` — активный участник не имеет нужной роли;
- `404` — дерево отсутствует либо пользователь не состоит в нём;
- `409` — устаревшая версия или недопустимое состояние восстановления.

## Локальный запуск и тесты

```powershell
docker compose up -d --wait family-postgres
go run ./cmd/migrate up
$env:FAMILY_TEST_DATABASE_URL="postgres://family_tree:family_tree@localhost:5434/family_tree_test?sslmode=disable"
go test ./...
```

Интеграционные тесты создают отдельную PostgreSQL schema для каждого запуска и отказываются работать с базой, в имени которой нет `test`.
