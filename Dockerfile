# syntax=docker/dockerfile:1

# ── build stage ──────────────────────────────────────────────────────
FROM golang:1.26-alpine AS build

WORKDIR /src

# Cache module downloads across source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev

# CGO disabled — the deps (pgx, std lib) are pure Go, so the binary is
# fully static and runs on scratch/alpine without libc.
RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/clowk-hep3 \
      ./cmd/clowk-hep3

# ── runtime stage ────────────────────────────────────────────────────
FROM alpine:3.20

# Pinned uid/gid 10001: the voodu-hep3 reader shares /data and must run as
# the SAME uid to read the 0600 NDJSON files.
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -g 10001 -S hep \
 && adduser -u 10001 -S -G hep hep \
 && mkdir -p /data \
 && chown hep:hep /data

COPY --from=build /out/clowk-hep3 /usr/local/bin/clowk-hep3

# /data: the default HEP_DATA_DIR for the ndjson store. The voodu-hep3
# reader shares this volume (read-only) and must run as the same uid.
VOLUME /data
USER hep

# 9060: HEP capture ingest over UDP and TCP (TCP recommended cross-VM).
EXPOSE 9060/udp
EXPOSE 9060/tcp

ENTRYPOINT ["/usr/local/bin/clowk-hep3"]
