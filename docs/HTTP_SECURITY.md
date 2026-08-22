# HTTP browser security perimeter

Статус: реализован второй hardening-срез Этапа 11. Общий middleware подключён снаружи API router после request ID, logger и panic recovery, поэтому политика применяется к health, успешным ответам, transport errors и CORS preflight.

## Response headers

Каждый ответ получает:

- `Cache-Control: no-store` для приватных account/family payloads и presigned URLs;
- `Content-Security-Policy: default-src 'none'; base-uri 'none'; frame-ancestors 'none'`;
- `X-Content-Type-Options: nosniff`;
- `X-Frame-Options: DENY` как legacy defense in depth;
- `Referrer-Policy: no-referrer`;
- отключение browser features через `Permissions-Policy`;
- `X-XSS-Protection: 0`, поскольку устаревший browser filter может сам создавать небезопасное поведение;
- опциональный HSTS с `includeSubDomains`.

Набор следует рекомендациям [OWASP HTTP Headers Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/HTTP_Headers_Cheat_Sheet.html). HSTS по умолчанию выключен, чтобы локальный HTTP не закреплялся браузером. В production за HTTPS ingress установить, например, `HTTP_HSTS_MAX_AGE_SECONDS=31536000`; включать `preload` без отдельного domain-wide решения нельзя.

## Credentialed CORS

`HTTP_ALLOWED_ORIGINS` содержит comma-separated точные origins без path, query или wildcard:

```dotenv
HTTP_ALLOWED_ORIGINS=https://family.example,https://admin.family.example
HTTP_CORS_MAX_AGE_SECONDS=600
```

Same-origin разрешён автоматически. Для allowlisted cross-origin запроса backend отражает только проверенный origin, возвращает `Access-Control-Allow-Credentials: true`, `Vary: Origin` и открывает клиенту `X-Request-ID`. Wildcard `*`, opaque `null`, пользовательские regex/suffix и неизвестные preflight headers запрещены. Это соответствует credentialed CORS: cookie нельзя сочетать с wildcard origin, а динамический explicit origin требует `Vary: Origin` ([MDN CORS](https://developer.mozilla.org/en-US/docs/Web/HTTP/Guides/CORS)).

Разрешённые browser request headers: `Authorization`, `Content-Type`, `X-CSRF-Protection`, `X-Request-ID`; методы: `GET`, `POST`, `PUT`, `PATCH`, `DELETE`. `OPTIONS` обслуживается middleware до route mux и не запускает use case.

## CSRF origin verification

Refresh cookie уже имеет `HttpOnly`, `Secure` в production и `SameSite=Strict`. Дополнительно для любого state-changing `POST`/`PUT`/`PATCH`/`DELETE` middleware:

1. нормализует `Origin`, либо использует origin из `Referer` как fallback;
2. принимает только exact configured origin или фактический same-origin target;
3. отклоняет `Sec-Fetch-Site: cross-site`, даже если `Origin`/`Referer` отсутствуют;
4. возвращает `403 forbidden` до вызова route handler.

Origin/Referer нельзя подменить из browser JavaScript, поэтому их строгая проверка вместе с SameSite cookie является рекомендуемой защитой в глубину ([OWASP CSRF Prevention Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)). Запрос без всех browser provenance headers разрешён для CLI/service clients; browser frontend должен отправлять обычный `Origin`, а при cross-origin fetch использовать credentials и пройти CORS preflight. Заголовок `X-CSRF-Protection` зарезервирован в allowlist для следующего double-submit/synchronizer-token среза, если deployment перестанет удовлетворять строгим SameSite/origin условиям.

За TLS reverse proxy публичный frontend/API origin нужно явно добавить в `HTTP_ALLOWED_ORIGINS`: внутренний HTTP scheme приложения не должен использоваться для вывода внешнего origin из непроверенных forwarded headers.

## Проверка

```powershell
go test ./internal/core/transport/http/middleware -count=1
go test ./... -count=1
go vet ./...
```

Unit-тесты покрывают security headers/HSTS, exact credentialed CORS, успешный preflight, недопустимые origin/config/header, cross-site state change, Fetch Metadata fallback и совместимость same-origin/CLI.
