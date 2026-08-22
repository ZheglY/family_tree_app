# syntax=docker/dockerfile:1@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32

FROM --platform=$BUILDPLATFORM golang:1.26.2-alpine3.23@sha256:f85330846cde1e57ca9ec309382da3b8e6ae3ab943d2739500e08c86393a21b1 AS build

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    GOPROXY=$GOPROXY GOSUMDB=$GOSUMDB go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -buildid=" -o /out/family-api ./cmd/family_tree_app && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -buildid=" -o /out/family-worker ./cmd/worker && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -buildid=" -o /out/family-migrate ./cmd/migrate && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -buildid=" -o /out/family-restore-backup ./cmd/restore-backup

FROM alpine:3.23@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40

ARG VERSION=dev
ARG VCS_REF=unknown

LABEL org.opencontainers.image.title="Family Tree backend" \
      org.opencontainers.image.description="Family Tree API, worker, migrations and backup restore tools" \
      org.opencontainers.image.source="https://github.com/ZheglY/family_tree_app" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$VCS_REF

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S -g 10001 family && \
    adduser -S -D -H -u 10001 -G family family

WORKDIR /app

COPY --from=build --chown=10001:10001 /out/family-api /app/family-api
COPY --from=build --chown=10001:10001 /out/family-worker /app/family-worker
COPY --from=build --chown=10001:10001 /out/family-migrate /app/family-migrate
COPY --from=build --chown=10001:10001 /out/family-restore-backup /app/family-restore-backup

ENV LOGGER_FORMAT=json \
    LOGGER_FOLDER=/tmp/family-tree-logs

USER 10001:10001

EXPOSE 8080 9090 9091

ENTRYPOINT ["/app/family-api"]
