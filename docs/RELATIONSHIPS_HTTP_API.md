# Parent-child relationships and graph API

Статус: реализованный контракт Этапа 6. Все пути имеют префикс `/api/v1`, требуют Bearer access token и ограничены активным membership указанного дерева.

## Инварианты графа

- родитель и ребёнок — разные активные персоны одного дерева;
- типизированный активный дубликат запрещён;
- новая направленная связь `parent → child` не может замкнуть существующий путь `child → ... → parent`;
- soft-deleted связи не участвуют в проверке циклов и graph-ответе;
- `owner` и `editor` изменяют связи, `viewer` только читает;
- outsider получает `404`, даже если знает UUID дерева, персон или связи.

Проверка цикла и вставка выполняются в одной PostgreSQL-транзакции. Все изменения графа одного дерева сериализуются transaction-scoped advisory lock. Это закрывает гонку, когда два параллельных запроса пытаются одновременно добавить встречные связи.

## Создать связь

`POST /trees/{treeID}/parent-child-relations`

```json
{
  "parent_person_id": "f6cc2452-246a-41f2-a025-80259fe03ab3",
  "child_person_id": "75c8b77a-97aa-47aa-8791-697369ce837e",
  "relation_type": "biological",
  "confidence": "confirmed",
  "note": "Подтверждено семейным архивом"
}
```

Ответ: `201 Created`, начальная версия равна `1`.

Допустимые типы:

- `biological`;
- `adoptive`;
- `foster`;
- `guardian`;
- `step`;
- `unknown` — значение по умолчанию.

Достоверность: `unverified`, `probable`, `confirmed`, `disputed`. По умолчанию используется `unverified`.

У одной пары могут существовать разные типы связи, например исторически спорная biological и подтверждённая adoptive. Дубликатом считается совпадение дерева, родителя, ребёнка и типа среди активных записей.

## Получить связь

`GET /trees/{treeID}/parent-child-relations/{relationID}`

```json
{
  "relation": {
    "id": "8b06f391-7630-4602-9191-73e610c3a25f",
    "tree_id": "3f7e35d0-35b5-46f9-be4a-6d388cf05a32",
    "parent_person_id": "f6cc2452-246a-41f2-a025-80259fe03ab3",
    "child_person_id": "75c8b77a-97aa-47aa-8791-697369ce837e",
    "relation_type": "biological",
    "confidence": "confirmed",
    "note": "Подтверждено семейным архивом",
    "created_by": "0c1a433b-8879-420b-85bc-0028dc29451f",
    "created_at": "2026-08-20T18:00:00Z",
    "updated_at": "2026-08-20T18:00:00Z",
    "version": 1
  },
  "access": {
    "role": "owner",
    "status": "active"
  }
}
```

Связь скрывается, если она, одна из её персон или само дерево мягко удалены.

## Изменить связь

`PATCH /trees/{treeID}/parent-child-relations/{relationID}`

```json
{
  "version": 1,
  "confidence": "probable",
  "note": "Нужна дополнительная проверка"
}
```

Изменяемые поля: `relation_type`, `confidence`, `note`. Родитель и ребёнок неизменяемы: ошибочную направленность нужно удалить и создать заново. Устаревшая версия возвращает `409 Conflict`.

## Удалить связь

`DELETE /trees/{treeID}/parent-child-relations/{relationID}`

```json
{
  "version": 2
}
```

Удаление мягкое, увеличивает версию и транзакционно создаёт `parent_child_relation.deleted` в `audit_log`. Отдельного restore endpoint на этом этапе нет; корректную активную связь можно создать заново.

## Получить фрагмент графа

`GET /trees/{treeID}/graph`

Параметры:

- `center_person_id` — обязательный UUID активной персоны;
- `ancestors_depth` — глубина предков от `0` до `6`, по умолчанию `2`;
- `descendants_depth` — глубина потомков от `0` до `6`, по умолчанию `2`;
- `include_partners` — принимается контрактом; союзы появятся на Этапе 7.

Ответ плоский, без рекурсивного дублирования персон:

```json
{
  "center_person_id": "75c8b77a-97aa-47aa-8791-697369ce837e",
  "persons": [],
  "parent_child_relations": [],
  "unions": [],
  "union_members": [],
  "include_partners": false,
  "access": {
    "role": "viewer",
    "status": "active"
  }
}
```

Персоны представлены краткими карточками с preferred-именем. Graph query выполняется в read-only repeatable-read транзакции. Сервер возвращает не более `500` уникальных персон; превышение лимита даёт `422`, после чего клиенту следует уменьшить глубину.

## Ошибки

- `400` — некорректные значения, UUID или глубина;
- `401` — отсутствует действительный access token;
- `403` — Viewer пытается изменить граф;
- `404` — дерево, персона или связь отсутствуют либо недоступны;
- `409` — дубликат или устаревшая версия;
- `422` — связь создаёт цикл либо graph query превышает лимит узлов.
