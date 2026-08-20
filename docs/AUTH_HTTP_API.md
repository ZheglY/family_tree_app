# Family API authentication boundary

Family API is the browser-facing BFF for the internal `identity.v1.IdentityService`. Browsers never call gRPC directly.

## HTTP endpoints

All endpoints use JSON under `/api/v1`:

| Method | Path | Authentication | Result |
|---|---|---|---|
| `POST` | `/auth/register` | public | Creates a pending account and requests email verification |
| `POST` | `/auth/verify-email` | public | Consumes a one-time verification token |
| `POST` | `/auth/login` | public | Returns an access token and sets the refresh cookie |
| `POST` | `/auth/refresh` | refresh cookie | Rotates the refresh token and returns a new access token |
| `POST` | `/auth/logout` | refresh cookie if present | Revokes one session and clears the cookie |
| `POST` | `/auth/logout-all` | bearer access token | Revokes every session owned by the authenticated user |
| `POST` | `/auth/forgot-password` | public | Returns a generic `202` and, for an active account, sends a reset link |
| `POST` | `/auth/reset-password` | public | Consumes a reset token, changes the password and revokes all sessions |
| `GET` | `/users/me` | bearer access token | Returns the current account profile |
| `GET` | `/users/me/sessions` | bearer access token | Lists active, non-expired sessions and marks the current one |
| `DELETE` | `/users/me/sessions/{session_id}` | bearer access token | Revokes one session owned by the current user |
| `POST` | `/users/me/change-password` | bearer access token plus current password | Changes the password and revokes all sessions |

Login request example:

```json
{
  "email": "family@example.com",
  "password": "correct horse battery staple"
}
```

Login and refresh return only the short-lived access token in JSON:

```json
{
  "user": {
    "id": "e7d360f4-2517-4e1d-923a-e76fc35a344f",
    "email": "family@example.com",
    "display_name": "Family Member",
    "status": "active"
  },
  "access_token": "...",
  "access_token_expires_at": "2026-08-17T12:15:00Z"
}
```

The refresh token is never included in JSON. It is stored in an `HttpOnly` cookie whose default `SameSite` policy is `Strict`. Local HTTP development defaults `Secure` to false; every HTTPS deployment must set `AUTH_REFRESH_COOKIE_SECURE=true`.

Password reset tokens are random, single-use and expire after one hour. A newer recovery request invalidates the previous token. Both password-changing endpoints clear the browser refresh cookie, and Identity revokes all server-side sessions. Because access tokens are locally validated JWTs, an already issued access token can remain valid until its short 15-minute expiry; clients must discard it after a successful password change or reset.

## Access-token validation

At startup, Family API requests the active public key and validation metadata from Identity Service. It then validates access tokens locally without a gRPC call on every protected request.

Validation requires:

- Ed25519/`EdDSA` and no alternative algorithm;
- the expected `kid` and `typ=at+jwt` headers;
- the expected issuer and audience;
- `exp`, `iat`, `nbf`, `sub`, `sid`, and `token_use=access` claims;
- UUID values for the user and session identifiers.

The current MVP supports one active signing key. Production key rotation with an overlap window is a later hardening task.

## Authentication rate limiting

Public authentication attempts are limited before Identity is called. Login, registration and password recovery use both an IP bucket and an HMAC-obfuscated account bucket; verification, refresh and reset use an IP bucket. Authenticated password changes are limited by IP and user ID. Rejected requests return `429 too_many_requests` with `Retry-After`; a limiter storage failure returns `503` and fails closed.

Default policies:

| Operation | IP bucket | Account bucket |
|---|---:|---:|
| register | 10/hour | 3/hour |
| login | 30/minute | 10/minute |
| verify email | 20/minute | — |
| refresh | 60/minute | — |
| forgot password | 20/hour | 3/hour |
| reset password | 10/minute | — |
| change password | 20/minute | 5/minute |

The default in-memory backend is bounded and suitable for one development process. Multi-instance deployments must set `AUTH_RATE_LIMIT_BACKEND=redis` and provide the same secret of at least 32 bytes through `AUTH_RATE_LIMIT_KEY_SECRET`. Redis receives only HMAC-derived subjects, never raw emails, user IDs, IP addresses, passwords or tokens. Start the local Redis adapter with `docker compose up -d --wait auth-redis`.

The IP subject currently comes from the direct socket peer (`RemoteAddr`); untrusted forwarding headers are deliberately ignored. A reverse proxy deployment must preserve the source address or add a trusted-proxy allowlist before public release, otherwise all clients behind one proxy would share its IP bucket.

## Internal gRPC client

Every Identity call has a configured deadline and propagates the HTTP request ID as `x-request-id` metadata. `Unavailable` and deadline failures become the standard HTTP `503 service_unavailable` envelope. Automatic retries are not enabled for state-changing operations.

Configuration:

```text
IDENTITY_GRPC_ADDR=localhost:50051
IDENTITY_GRPC_TIMEOUT=3s
IDENTITY_GRPC_TLS_ENABLED=false
IDENTITY_GRPC_TLS_SERVER_NAME=
IDENTITY_GRPC_CA_FILE=
```

The client supports TLS 1.3 and an optional private CA. The Identity server still needs its matching authenticated transport configuration before production deployment.

## Cross-service smoke test

With the local Identity PostgreSQL container running, execute:

```powershell
./scripts/e2e-auth-smoke.ps1
```

The script builds temporary binaries, migrates the Identity and Family test databases, starts both services in hidden processes, and exercises registration, email verification, login, refresh, session management, family-tree and person lifecycles, authenticated password change, password recovery and a real `429` login limit over the HTTP and gRPC transports. It stops the processes and removes its temporary binaries and logs on completion.

## Protobuf contract ownership

`api/proto/identity/v1/identity.proto` is a consumer snapshot of the contract owned by Identity Service. When that source contract changes, update this snapshot, regenerate `gen/identity/v1`, and commit both repositories in the same development stage. A separately versioned contract module can replace this duplication once independent releases are introduced.
