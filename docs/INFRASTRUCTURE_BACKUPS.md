# Infrastructure backups and restore drills

Статус: инфраструктурный backup/restore drill Этапа 11 реализован для Family PostgreSQL и private S3 bucket. Пользовательский `zip_backup` одного дерева остаётся отдельным application-level механизмом.

## Два уровня восстановления

1. `zip_backup` переносит одно дерево вместе с media и проверяется доменными инвариантами. Он не содержит Identity Service, audit log, очередь и историю export jobs.
2. Infrastructure backup сохраняет всю Family PostgreSQL и все текущие private S3 objects. Он предназначен для disaster recovery среды целиком и не заменяет отдельный backup Identity Service.

PostgreSQL сохраняется `pg_dump` 17 в custom format с `--no-owner --no-privileges`, затем восстанавливается `pg_restore --single-transaction --exit-on-error`. Роли, владельцы database и права создаются deployment/IaC до restore. S3 objects скачиваются по безопасным application keys, для каждого повторно проверяются размер и metadata `sha256`; manifest также сохраняет content type. При restore metadata восстанавливается и каждый объект повторно скачивается для SHA-256 проверки.

## Исполняемый drill

Требования:

- `pg_dump`, `pg_restore` и `psql` версии 17 или новее, либо Docker на Linux и
  образ PostgreSQL client той же major-версии через `-PostgresClientMode docker`;
- AWS CLI v2, настроенный статическим/временным ключом с минимальными правами на исходный и drill buckets;
- пустая целевая PostgreSQL database и пустой private S3 bucket;
- остановленные Family API и worker либо другой подтверждённый maintenance barrier, исключающий запись;
- свободное локальное место не меньше custom dump плюс полный объём текущих S3 objects.

Пример:

```powershell
$env:AWS_ACCESS_KEY_ID = '<backup-service-account-key>'
$env:AWS_SECRET_ACCESS_KEY = '<backup-service-account-secret>'
./scripts/infrastructure-backup-drill.ps1 `
  -BackupDirectory 'D:\family-tree-backups\2026-08-22' `
  -SourcePostgresURL $env:POSTGRES_URL `
  -TargetPostgresURL 'postgres://restore:***@restore-host:5432/family_tree_drill?sslmode=require' `
  -SourceBucket 'family-tree-media-production' `
  -TargetBucket 'family-tree-media-restore-drill' `
  -S3Endpoint 'https://storage.yandexcloud.net' `
  -S3Region 'ru-central1' `
  -ConfirmQuiesced
```

Для создания backup artifact без немедленного restore используется `-CreateOnly`. Каталог должен быть новым или пустым; script никогда не очищает source/target database или bucket. Drill отказывается писать в непустые targets и оставляет успешно восстановленные данные для последующего аудита. Manifest не содержит DSN или credentials, но dump, object bytes и manifest содержат приватные семейные данные и должны храниться в зашифрованном, access-controlled backup storage.

Drill проверяет:

- SHA-256 custom dump;
- migration version и точные counts всех 14 Family tables до и после restore;
- наличие `sha256` metadata и content type каждого исходного object;
- byte size и SHA-256 скачанного backup object;
- пустоту restore targets до записи;
- metadata, размер и повторно скачанный SHA-256 каждого восстановленного object.

CI создаёт дерево и checksummed S3 object, выполняет migrations, dump, restore в новую database и копирование в отдельный MinIO bucket. Поэтому script и фактические версии `pg_dump`/`pg_restore`/S3 API проверяются на каждом push и pull request.

GitHub Actions использует `postgres:17-alpine` как одноразовый client container,
подключённый к host network runner. Это сохраняет совпадение major-версии server и
client без зависимости от того, опубликован ли `postgresql-client-17` в APT-репозитории
конкретного образа runner.

## Production policy

Минимальная политика до staging:

- ежедневный полный infrastructure backup с retention 30 дней;
- еженедельный автоматический restore drill в изолированные targets;
- ежемесячный ручной аудит доступности, времени восстановления и выборочная проверка UI/API после restore;
- отдельные backup credentials без доступа публичного приложения;
- копия вне primary bucket/account; один только replicated Object Storage не считается backup;
- versioning на primary и backup buckets, lifecycle для non-current versions и незавершённых multipart uploads;
- object lock в governance/compliance mode для backup bucket после согласования retention;
- отдельная политика backup/restore Identity PostgreSQL, иначе сохранённые `user_id` не вернут аккаунты.

Текущий quiesced drill даёт проверяемый базовый RPO до одного backup interval (24 часа) и измеряемый RTO. Перед публичным production release целевые RPO/RTO должны быть утверждены, а PostgreSQL переведён на managed PITR/WAL archiving или эквивалент. S3 versioning защищает от случайного overwrite/delete, но независимая копия всё равно обязательна.

После каждого drill фиксируются timestamp, размер PostgreSQL dump, число/объём objects, длительность backup, длительность restore, результат проверок и ответственный оператор. Credentials, DSN, object keys и содержимое семейных данных в отчёт не включаются.
