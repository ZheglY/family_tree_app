# Observability

Статус: observability-срез Этапа 11 реализован для Family API и PostgreSQL worker.

## Prometheus listeners

Метрики не добавлены в публичный Family API. Каждый процесс поднимает отдельный HTTP listener только с `GET /metrics`:

- Family API: `API_METRICS_ENABLED=true`, `API_METRICS_ADDR=127.0.0.1:9090`;
- worker: `WORKER_METRICS_ENABLED=true`, `WORKER_METRICS_ADDR=127.0.0.1:9091`.

Loopback defaults безопасны для локального запуска. В контейнерном staging/production адрес меняется на внутренний container interface, а network policy и Prometheus scrape configuration оставляют listener недоступным из публичной сети. Ошибка запуска metrics listener завершает весь процесс; graceful shutdown выполняется вместе с основным HTTP server или worker.

Основные серии:

- `family_tree_http_requests_total{method,route,status}` и `family_tree_http_request_duration_seconds{method,route}`;
- `family_tree_http_requests_in_flight`;
- `family_tree_postgres_connections{state}`, acquire/destroy counters и суммарное acquire time;
- `family_tree_job_queue_jobs{status}` и возраст старейшего runnable job;
- worker claim errors, claimed jobs, controlled outcomes и job duration;
- стандартные Go runtime и process metrics.

HTTP label `route` содержит шаблон зарегистрированного маршрута, например `/api/v1/trees/{tree_id}`, а не raw URL. Query string, UUID конкретного ресурса, токены, email, filenames и request/response bodies в labels не попадают. Job `kind`, `outcome`, queue `status` и PostgreSQL `state` имеют закрытое, ограниченное множество значений.

Локальная проверка:

```powershell
Invoke-WebRequest http://127.0.0.1:9090/metrics
Invoke-WebRequest http://127.0.0.1:9091/metrics
```

## Structured logging

`LOGGER_FORMAT=json` — значение по умолчанию; `console` оставлен для локальной интерактивной разработки. HTTP completion log содержит только `request_id`, method, route template, status и latency. Raw URL/query, Authorization/Cookie headers и body не логируются. Для ожидаемых 4xx ошибок пишется только контролируемый `error_code`, без обёрнутого сообщения, которое могло содержать пользовательский ввод. Неожиданный panic не сериализует panic value.

В worker разрешены служебные `worker_id`, `job_id`, закрытый `job_kind`, attempt и controlled outcome. Payload очереди и доменные person/user/tree данные не логируются. Новые поля должны проходить тот же аудит до добавления.
