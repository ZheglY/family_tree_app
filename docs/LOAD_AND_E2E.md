# Large graph load gate and cross-service E2E

Статус: нагрузочный/E2E-срез Этапа 11 реализован.

## Large graph regression gate

`TestLargeGraphReadLoadBudget` создаёт в изолированной PostgreSQL schema дерево на 10 000 персон. В центре находится связный трёхветвевой граф из 364 персон и 363 отношений в пределах максимальной глубины API; остальные записи проверяют, что запрос не деградирует от общего размера tenant tree.

После warm-up тест одновременно выполняет 12 максимальных graph reads и проверяет:

- каждый ответ содержит ровно ожидаемые persons/relations;
- ни один запрос не превышает context deadline;
- p95 по 12 конкурентным запросам не превышает 3 секунд;
- tenant authorization и `MaxGraphNodes=500` остаются частью реального repository path.

Запуск:

```powershell
$env:FAMILY_TEST_DATABASE_URL='postgres://family_tree:family_tree@localhost:5434/family_tree_test?sslmode=disable'
go test ./internal/features/relationships/repository/postgres `
  -run TestLargeGraphReadLoadBudget -count=1 -v -timeout=120s
```

Порог 3 секунды — стабильный CI regression budget на shared runner, а не production SLO или доказательство предельной ёмкости. На staging дополнительно фиксируются p50/p95/p99 HTTP latency, error rate, PostgreSQL acquire wait, CPU/RAM и queue lag на согласованном профиле нагрузки.

## Cross-service E2E smoke

`scripts/e2e-auth-smoke.ps1` собирает и запускает Family API, worker и отдельный Identity gRPC service, применяет обе схемы test database и проходит реальный пользовательский сценарий:

- registration, email verification, login/refresh/logout, session revoke, password change/reset и rate limit;
- tree/person lifecycle, tenant authorization, graph cycle protection и family unions;
- presigned private media upload, worker processing, variants, attachment и primary photo;
- JSON и ZIP backups, checksums, private download и cleanup states;
- liveness/readiness, API/worker Prometheus listeners, bounded route labels и отсутствие UUID/email в metrics.

Требуются оба соседних repository и запущенные PostgreSQL/MinIO dependencies. Запуск из Family repository:

```powershell
docker compose up -d --wait family-postgres media-s3
./scripts/e2e-auth-smoke.ps1 `
  -IdentityRepository '..\family-tree-identity-microservice'
```

Smoke использует только `*_test` databases, отдельные HTTP/gRPC/metrics ports и временный каталог. Перед release candidate его итоговый объект и версии обоих commits прикладываются к release evidence. Development mailer используется только для получения одноразовых test tokens; production mailer остаётся обязательным отдельным release prerequisite.
