# syntax=docker/dockerfile:1

# ── build stage ──────────────────────────────────────────────────────
FROM golang:1.25-alpine AS build

WORKDIR /src

# Cache module downloads across source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev

# CGO disabled — modernc.org/sqlite is pure Go, so the binary is fully
# static and runs on scratch/alpine without libc.
RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/clowk-hep3 \
      ./cmd/clowk-hep3

# ── runtime stage ────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S hep \
 && adduser -S -G hep hep \
 && mkdir -p /data \
 && chown hep:hep /data

COPY --from=build /out/clowk-hep3 /usr/local/bin/clowk-hep3

VOLUME /data
USER hep

# 9060/udp: HEP capture ingest.
EXPOSE 9060/udp

ENTRYPOINT ["/usr/local/bin/clowk-hep3"]
