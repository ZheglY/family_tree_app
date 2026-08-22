# Восстановление ZIP backup

Статус: реализован проверяемый offline restore для `zip_backup` schema v1 (Этап 10.3). Это операторский сценарий аварийного восстановления, а не публичный пользовательский import endpoint.

Полное восстановление всей Family PostgreSQL и private S3 bucket описано отдельно в [`INFRASTRUCTURE_BACKUPS.md`](INFRASTRUCTURE_BACKUPS.md). Эти два уровня backup решают разные задачи и не заменяют друг друга.

## Что восстанавливается

Команда сохраняет UUID и исторические поля исходного дерева и создаёт заново:

- `FamilyTree` и все memberships;
- персон и имена;
- parent-child relations;
- семейные союзы и их участников;
- metadata медиа, варианты и привязки к персонам;
- оригиналы и варианты медиа в private S3;
- root person, cover media и primary media pointers.

Исходный audit log, фоновые задания и `export_jobs` намеренно не входят в backup. После успешного восстановления создаётся новая audit entry `backup.restored` с ID исходного экспорта.

Identity Service не восстанавливается этим архивом. UUID пользователей сохраняются, поэтому соответствующие аккаунты должны существовать или восстанавливаться отдельно из backup Identity Service.

## Предварительные условия

- целевая PostgreSQL уже развёрнута актуальными миграциями;
- private S3 bucket доступен;
- дерево с тем же UUID отсутствует;
- детерминированные S3 keys восстанавливаемых файлов отсутствуют;
- размер архива не превышает `EXPORT_MAX_ARCHIVE_BYTES`.

База не обязана быть полностью пустой: restore безопасно отказывается продолжать при конфликте UUID дерева, глобальных UUID записей или S3 object keys. Для регулярного restore drill рекомендуется отдельная чистая тестовая база и отдельный bucket.

## Запуск

Настроить `POSTGRES_*`, `S3_*`, `LOGGER_*` и выполнить:

```powershell
go run ./cmd/restore-backup .\family-tree-backup.zip
```

Успешная команда выводит только UUID дерева, число восстановленных объектов и число поставленных media jobs. Содержимое архива и персональные данные в лог не записываются.

## Проверки архива

До изменения PostgreSQL или S3 команда проверяет:

- ZIP structure, schema name/version и формат manifest;
- ограничение общего сжатого и распакованного размера и не более 10 000 entries;
- отсутствие duplicate, directory и небезопасных paths;
- точное соответствие entries файлу `checksums.sha256`;
- SHA-256, размер и canonical UUID-based path каждого файла;
- отсутствие неизвестных или не указанных в manifest файлов;
- уникальность IDs, целостность ссылок и отсутствие циклов в активном parent-child graph;
- одного активного owner и ровно одно preferred name у каждой персоны.

Файлы не распаковываются на файловую систему: проверенные bytes передаются напрямую в private S3.

## Атомарность и состояния медиа

Доменные записи создаются одной PostgreSQL-транзакцией. S3 не поддерживает общую транзакцию с PostgreSQL, поэтому restore:

1. отказывается перезаписывать существующие object keys;
2. запоминает только загруженные текущим запуском objects;
3. при любой ошибке откатывает PostgreSQL и удаляет эти objects в обратном порядке;
4. при неоднозначном ответе `COMMIT` подтверждает успех через `backup.restored` и ID исходного экспорта.

Состояния медиа нормализуются для безопасного продолжения после восстановления:

| Состояние в backup | Состояние после restore | Действие |
|---|---|---|
| `pending` | `rejected` | незавершённая загрузка не восстанавливается |
| `uploaded` | `uploaded` | создаётся `media.process` |
| `processing` | `uploaded` | обработка безопасно запускается заново |
| `ready` | `ready` | оригинал и варианты остаются готовыми |
| `rejected` | `rejected` | причина заменяется безопасным техническим сообщением |
| `deleted` | `deleted` | soft-delete сохраняется |

Чтобы не потерять in-flight upload, ZIP включает оригиналы активных медиа в состояниях `uploaded`, `processing` и `ready`, а также существующие варианты этих медиа.

## Проверка

Полный integration test восстанавливает архив в изолированную PostgreSQL schema и реальный S3-compatible MinIO, затем скачивает objects и повторно сравнивает SHA-256:

```powershell
$env:FAMILY_TEST_DATABASE_URL="postgres://family_tree:family_tree@localhost:5434/family_tree_test?sslmode=disable"
$env:S3_TEST_ENDPOINT="http://localhost:9000"
go test ./internal/features/exports/restore -v -count=1
```

Публичный HTTP import остаётся отдельным продуктовым решением: перед его добавлением понадобятся mapping владельца и участников, preview/conflict policy и tenant-scoped authorization.
