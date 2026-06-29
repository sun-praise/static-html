# syntax=docker/dockerfile:1.7
#
# Multi-stage build for the `sth` static-html preview server.
#
# Build args (consumed by CI):
#   VERSION     - semver tag (e.g. 1.3.0); used in binary ldflags and OCI label
#   COMMIT      - git SHA; informational
#   BUILD_DATE  - RFC3339 timestamp; used in OCI label
#

# ---- Build stage ---------------------------------------------------------
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app

# Cache go modules before copying sources
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=1 GOOS=linux \
    go build \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
      -o /out/html-server \
      ./cmd/html-server

# ---- Runtime stage -------------------------------------------------------
FROM alpine:3.21

# ca-certificates: outbound HTTPS calls (e.g. fetch metadata)
# sqlite:          hot backup via `sqlite3 .backup` (run from host)
# tini:            PID 1 signal forwarding / zombie reaping
RUN apk add --no-cache ca-certificates sqlite tini

RUN adduser -D -u 1000 appuser

# Pre-create the volume mount points with appuser ownership.
# Docker copies the directory's owner/perms into a freshly-created named
# volume on first mount, so this lets the non-root process write to /data
# and /backup without an init container or entrypoint chown.
RUN mkdir -p /data /backup && chown -R 1000:1000 /data /backup

WORKDIR /app

COPY --from=builder /out/html-server /usr/local/bin/html-server

USER appuser

EXPOSE 3939

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:3939/ || exit 1

# OCI image labels (https://github.com/opencontainers/image-spec/blob/main/annotations.md)
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
LABEL \
  org.opencontainers.image.title="static-html" \
  org.opencontainers.image.description="Local HTML preview server with CLI" \
  org.opencontainers.image.source="https://github.com/sun-praise/static-html" \
  org.opencontainers.image.url="https://github.com/sun-praise/static-html" \
  org.opencontainers.image.documentation="https://github.com/sun-praise/static-html/blob/main/README.md" \
  org.opencontainers.image.licenses="Apache-2.0" \
  org.opencontainers.image.vendor="sun-praise" \
  org.opencontainers.image.version="${VERSION}" \
  org.opencontainers.image.revision="${COMMIT}" \
  org.opencontainers.image.created="${BUILD_DATE}"

ENTRYPOINT ["/sbin/tini", "--", "html-server"]
CMD ["start", "--bind", "0.0.0.0", "--port", "3939"]
