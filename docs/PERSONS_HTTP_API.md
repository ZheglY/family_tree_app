# Persons HTTP API

Статус: реализованный минимальный контракт Этапа 5. Все пути имеют префикс `/api/v1`, требуют Bearer access token и всегда ограничены `tree_id` вместе с активным membership.

## Доступ и агрегат

`Person` и её preferred `PersonName` образуют один минимальный агрегат. Создание и изменение двух записей выполняются в одной PostgreSQL-транзакции. Составной внешний ключ `(tree_id, person_id)` не позволяет привязать имя к персоне другого дерева.

- `owner` и `editor` могут создавать, изменять, удалять и восстанавливать персон;
- `viewer` может читать карточки и список;
- пользователь без membership получает `404`, даже если знает UUID дерева и персоны.

В первом срезе реализовано одно обязательное preferred-имя. Альтернативные имена, события рождения/смерти и primary media добавляются следующими специализированными API без изменения идентичности `Person`.

## Создать персону

`POST /trees/{treeID}/persons`

```json
{
  "sex": "female",
  "life_status": "alive",
  "biography": "Краткая биография",
  "notes": "Приватные рабочие заметки",
  "preferred_name": {
    "given_name": "Анна",
    "patronymic": "Петровна",
    "family_name": "Волконская",
    "prefix": "",
    "suffix": "",
    "language_code": "ru"
  }
}
```

Хотя бы одна часть имени должна быть непустой. По умолчанию `sex = not_specified`, `life_status = unknown`, `language_code = ru`. Ответ: `201 Created`, версия персоны равна `1`.

Допустимые значения:

- `sex`: `male`, `female`, `unknown`, `not_specified`;
- `life_status`: `alive`, `deceased`, `unknown`;
- `privacy_level` в MVP всегда `tree_members`.

## Получить карточку

`GET /trees/{treeID}/persons/{personID}` возвращает:

```json
{
  "person": {
    "id": "8c71d232-e4d9-40f2-ab28-f45550c38232",
    "tree_id": "fd17aac5-a0ae-47cb-9ce7-ed40e60916bf",
    "sex": "female",
    "life_status": "alive",
    "biography": "Краткая биография",
    "notes": "",
    "privacy_level": "tree_members",
    "created_by": "3fc9d323-ea69-45d9-80b1-22ae64037a03",
    "updated_by": "3fc9d323-ea69-45d9-80b1-22ae64037a03",
    "created_at": "2026-08-20T15:00:00Z",
    "updated_at": "2026-08-20T15:00:00Z",
    "version": 1
  },
  "preferred_name": {
    "id": "62f25bef-cadc-40e2-b173-2c6f6722e610",
    "type": "primary",
    "given_name": "Анна",
    "patronymic": "Петровна",
    "family_name": "Волконская",
    "prefix": "",
    "suffix": "",
    "full_text": "Анна Петровна Волконская",
    "is_preferred": true,
    "language_code": "ru",
    "created_at": "2026-08-20T15:00:00Z",
    "updated_at": "2026-08-20T15:00:00Z"
  },
  "access": {
    "role": "owner",
    "status": "active"
  }
}
```

## Список и поиск

`GET /trees/{treeID}/persons`

Поддержанные параметры:

- `query` — регистронезависимый поиск подстроки в `full_text`;
- `life_status` — точный фильтр статуса жизни;
- `has_media=true|false` — фильтр по `primary_media_id`, становится полезен после media-среза;
- `limit` — от 1 до 100, по умолчанию 50;
- `cursor` — непрозрачный cursor из предыдущего ответа.

```json
{
  "items": [],
  "next_cursor": "eyJuIjoi0LDQvdC90LAiLCJpZCI6Ii4uLiJ9"
}
```

Сортировка стабильна по нормализованным пробелам preferred-имени и UUID. `born_from` и `born_to` появятся после реализации `GenealogyDate` и событий рождения; до этого API не создаёт второй источник истины для дат.

## Изменить персону

`PATCH /trees/{treeID}/persons/{personID}`

```json
{
  "version": 1,
  "biography": "Обновлённая биография",
  "preferred_name": {
    "given_name": "Анна",
    "patronymic": "Петровна",
    "family_name": "Болконская",
    "prefix": "",
    "suffix": "",
    "language_code": "ru"
  }
}
```

Поля персоны частичные. Если меняется `preferred_name`, передаётся его полное новое значение. Любое изменение агрегата увеличивает `Person.version`; устаревшая версия возвращает `409 Conflict`.

## Удалить и восстановить

`DELETE /trees/{treeID}/persons/{personID}` и `POST /trees/{treeID}/persons/{personID}/restore` принимают:

```json
{
  "version": 2
}
```

Удаление мягкое и скрывает персону из обычного чтения и поиска. Имя сохраняется. Удаление и восстановление атомарно записывают событие в `audit_log` и увеличивают версию.

## Ошибки

- `400` — JSON, значения, limit или cursor некорректны;
- `401` — нет действительного access token;
- `403` — активная роль не разрешает изменение;
- `404` — дерево/персона отсутствуют или недоступны пользователю;
- `409` — версия устарела либо восстановление вызвано для активной персоны.
