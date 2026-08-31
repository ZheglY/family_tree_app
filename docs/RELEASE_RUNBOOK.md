# Staging and release runbook

This runbook describes the platform-neutral release contract for the Family Tree
backend. The actual staging platform, ingress, secret manager and monitoring stack
must be selected before the first deployment.

## Release artifact

`Dockerfile` builds one minimal image containing four statically linked binaries:

- `/app/family-api` (the default entrypoint);
- `/app/family-worker`;
- `/app/family-migrate`;
- `/app/family-restore-backup`.

The runtime process uses UID/GID `10001`, has no Linux capabilities in the staging
Compose template and writes only below `/tmp`. The Dockerfile frontend, Go image and
Alpine image are pinned by multi-platform digest. Update their tags and digests
together when the builder, Go toolchain or Alpine receives a planned upgrade.

BuildKit caches downloaded modules and compiled packages without copying either cache
into the runtime image. `GOPROXY` and `GOSUMDB` are overridable build arguments for a
controlled corporate or offline Go proxy; release CI uses their public defaults.

Build and identify an artifact with the source revision:

```sh
docker build \
  --build-arg VERSION=v0.1.0 \
  --build-arg VCS_REF="$(git rev-parse HEAD)" \
  --tag registry.example/family-tree-backend:v0.1.0 .
```

Promote the exact registry digest tested in staging. Do not rebuild an image for
production and do not deploy mutable `latest` tags.

Pushing a Git tag such as `backend-v0.1.0-rc.1`, or manually dispatching the
`Backend release image` workflow with `v0.1.0-rc.1`, publishes the image to GHCR
under both the version and full source-revision tags. The workflow never writes a
`latest` tag. Resolve the published version to its digest before deployment; the
image may be mirrored to the registry selected for the staging platform.

## Required external services

- PostgreSQL 17 with TLS and an application-specific role;
- private Yandex Object Storage bucket with lifecycle/versioning policy and
  server-side encryption;
- Identity gRPC endpoint with certificate validation;
- Redis when more than one API replica handles auth rate limits;
- HTTPS ingress/reverse proxy;
- a Prometheus-compatible scraper for API `:9090` and worker `:9091` on the private
  network.

Copy `deploy/staging/staging.env.example` to a secret-managed path outside the
repository. Replace every placeholder, make the file readable only by the deploy
identity and never print it in CI logs.

## Deployment order

1. Record the image digest, configuration revision and current database migration
   version.
2. Verify a recent PostgreSQL/Object Storage backup with the restore drill.
3. Pull the immutable image and run `/app/family-migrate up` once.
4. Start or roll the API. Wait for `/health/ready` to succeed.
5. Start or roll the worker. Confirm its private `/metrics` endpoint and successful
   job claims.
6. Run the cross-service E2E smoke and inspect error rate, latency and queue age.
7. Keep the previous image digest available until the observation window closes.

For a single-host staging candidate, the checked-in Compose template enforces this
order:

```sh
export FAMILY_TREE_IMAGE=registry.example/family-tree-backend@sha256:replace-me
export STAGING_ENV_FILE=/secure/family-tree/staging.env
docker compose -f deploy/staging/compose.yaml up --detach
```

The API binds to loopback by default so an HTTPS reverse proxy can be its only public
entry point. Prometheus must join the Compose network to scrape the unexposed metrics
ports.

## Rollback

Application rollback means selecting the previous immutable image digest and rolling
the API and worker back. Never run migration `down` automatically. A rollback is safe
only while the newly applied schema remains backward compatible with the previous
application. A destructive or incompatible migration requires a separately reviewed
forward-fix or restore procedure.

Pause workers before a database restore. Restore PostgreSQL and the matching object
snapshot into an isolated environment first, validate checksums and tree counts, then
switch traffic under an incident plan.

## Staging acceptance evidence

The first real staging rollout is complete only when its release record contains:

- immutable image digest and migration version;
- healthy API/worker instances after a restart;
- cross-service E2E result against staging Identity;
- large-graph latency result;
- PostgreSQL and Object Storage restore-drill result;
- metrics/log screenshots or links proving bounded labels and no personal data;
- rollback rehearsal result;
- open exceptions, owner and expiry date.

Before a public production release, replace the development mail delivery path with a
production mailer and add authenticated service-to-service gRPC transport.
