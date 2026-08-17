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
| `GET` | `/users/me` | bearer access token | Returns the current account profile |
| `GET` | `/users/me/sessions` | bearer access token | Lists active, non-expired sessions and marks the current one |
| `DELETE` | `/users/me/sessions/{session_id}` | bearer access token | Revokes one session owned by the current user |

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

## Access-token validation

At startup, Family API requests the active public key and validation metadata from Identity Service. It then validates access tokens locally without a gRPC call on every protected request.

Validation requires:

- Ed25519/`EdDSA` and no alternative algorithm;
- the expected `kid` and `typ=at+jwt` headers;
- the expected issuer and audience;
- `exp`, `iat`, `nbf`, `sub`, `sid`, and `token_use=access` claims;
- UUID values for the user and session identifiers.

The current MVP supports one active signing key. Production key rotation with an overlap window is a later hardening task.

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

The script builds temporary binaries, migrates the `identity_test` database, starts both services in hidden processes, and exercises registration, email verification, login, refresh and logout-all over the real HTTP and gRPC transports. It stops the processes and removes its temporary binaries and logs on completion.

## Protobuf contract ownership

`api/proto/identity/v1/identity.proto` is a consumer snapshot of the contract owned by Identity Service. When that source contract changes, update this snapshot, regenerate `gen/identity/v1`, and commit both repositories in the same development stage. A separately versioned contract module can replace this duplication once independent releases are introduced.
