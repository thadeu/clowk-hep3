# Changelog

All notable changes to clowk-hep3 are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/) and the project adheres to
[Semantic Versioning](https://semver.org/). The first tagged release will
be `v0.1.0`.

## [Unreleased]

### Added

- HEP3 capture collector: UDP (and optional TCP) receiver, SIP parser,
  500ms-window dedup, and a batched writer.
- Postgres storage via `pgx`: one JSONB `data` column with STORED
  generated columns (`ts`, `call_id`, `x_cid`, `method`, `response_code`,
  `from_user`, `to_user`, `cseq`) and indexes.
- Embedded golang-migrate migrations (`infra/migrations`), applied on boot
  — clowk-hep3 owns the schema.
- B2BUA correlation: configurable `HEP_CID` (default `X-CID`),
  falling back to the HEP correlation chunk.
- Method discard filter (`HEP_EXCEPT_METHODS`) and time-based retention
  (`HEP_RETENTION_DAYS`).
- Config via env (caarlos0/env) + optional `.env` (godotenv); only
  `DATABASE_URL` is required.
- Static (CGO-free) binary, multi-arch image at
  `ghcr.io/thadeu/clowk-hep3`, and an example `hep3-server.voodu`
  deployment manifest.

### Notes

- clowk-hep3 is WRITE-ONLY. The read API is a separate service
  (voodu-hep3) against the same `DATABASE_URL`.
