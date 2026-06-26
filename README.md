# clowk-hep3

**HEP3 capture collector — receive SIP, persist it.**

`clowk-hep3` receives [HEP3](https://github.com/sipcapture/HEP) datagrams
from any HEP3 source — a SIP proxy (Kamailio, FreeSWITCH, OpenSIPS …) or a
capture agent (heplify) — extracts the SIP signaling, and **persists it**.
It is the WRITER / collector half of the SIP-capture stack. See
[Capture sources](#capture-sources).

By default it **appends NDJSON to a shared volume** (`HEP_STORE=ndjson`);
**Postgres** is an opt-in backend (`HEP_STORE=pg`), and you can dual-write
to both. See [Storage backends](#storage-backends).

It serves no API. The read-only REST/export layer (call lists, ladder
diagrams, stats) is a separate, independently-deployable service —
[voodu-hep3](https://github.com/thadeu/voodu-hep3) — that consumes the same
backend (the shared NDJSON volume, or the same `DATABASE_URL`).

> HEP3 is captured as a **side-channel**: your SIP source sends a copy of
> each message to clowk-hep3 in parallel; it never sits in the call path.

## Quick start

Default (NDJSON): give it a data volume and point your SIP source at it.

```bash
docker run \
  -p 9060:9060/udp -p 9060:9060/tcp \
  -e HEP_TCP_ADDR=0.0.0.0:9060 \
  -v hepdata:/data \
  ghcr.io/thadeu/clowk-hep3:latest
```

clowk-hep3 listens for HEP3 on UDP+TCP 9060 and appends parsed SIP as
NDJSON to `/data` (`HEP_DATA_DIR`), one hourly file per `sip-<hour>.ndjson`.
Point your SIP source's HEP capture at `<host>:9060` (TCP recommended — see
[docs/transport.md](docs/transport.md)).

On voodu, deploy it as a plain deployment — see
[examples/hep3-server.voodu](examples/hep3-server.voodu).

## Configuration

Everything is an env var with a working default; a local `.env` is loaded
for development. With the default `ndjson` store, **no var is required**.

| Var                  | Default        | Description                                   |
| -------------------- | -------------- | --------------------------------------------- |
| `HEP_STORE`          | `ndjson`       | write backend(s): `ndjson`, `pg`, or `pg,ndjson` (dual-write) |
| `HEP_DATA_DIR`       | `/data`        | directory the `ndjson` store appends to (shared volume) |
| `DATABASE_URL`       | _(unset)_      | Postgres connection string — **required only when `HEP_STORE` includes `pg`** |
| `HEP_ADDR`           | `0.0.0.0:9060` | UDP listen address for HEP3 datagrams         |
| `HEP_TCP_ADDR`       | _(disabled)_   | TCP listen address — **recommended for cross-VM** (reliable); set to enable. See [docs/transport.md](docs/transport.md) |
| `HEP_DB_BULK`        | `200`          | write batch size (both backends)              |
| `HEP_DB_TIMER`       | `4s`           | max wait before flushing a partial batch      |
| `HEP_DB_WORKERS`     | `4`            | parallel decode workers                       |
| `HEP_RETENTION_DAYS` | `30`           | drop data older than this (0 = keep all)      |
| `HEP_CID`            | `X-CID`        | SIP header used to stitch B2BUA leg pairs     |
| `HEP_EXCEPT_METHODS` | `OPTIONS`      | comma-separated SIP methods dropped on ingest |

> **Transport:** HEP3 runs over UDP (default) and TCP (`HEP_TCP_ADDR`, opt-in).
> For cross-VM captures, prefer TCP — it won't silently drop messages under
> network congestion. Both can share port 9060. See [docs/transport.md](docs/transport.md).

## Storage backends

`HEP_STORE` selects where parsed SIP is written. Both backends store the
**same record shape**, so the [voodu-hep3](https://github.com/thadeu/voodu-hep3)
reader can consume either.

- **`ndjson`** (default) — appends one JSON document per line to hourly
  files (`sip-<UTC-hour>.ndjson`) under `HEP_DATA_DIR`. Retention deletes
  whole files older than `HEP_RETENTION_DAYS`. The reader shares this
  volume **read-only** and must run as the **same uid** (files are `0600`,
  the dir `0750` — SIP is PII). No database to operate.
- **`pg`** — writes to Postgres. clowk-hep3 owns the schema (runs the
  embedded golang-migrate migrations on boot) and writes each message as
  one JSONB `data` row; the query-hot fields (`ts`, `call_id`, `x_cid`,
  `method`, `response_code`, `from_user`, `to_user`, `cseq`) are STORED
  generated columns over `data->>'...'`, and indexed. Requires
  `DATABASE_URL`.
- **`pg,ndjson`** — dual-write to both (e.g. NDJSON primary + Postgres warm
  standby during a migration). Each backend buffers independently.

The record fields (identical in both): `ts`, `call_id`, `x_cid`, `method`,
`response_code`, `from_user`, `to_user`, `ruri`, `src_ip`, `dst_ip`,
`src_port`, `dst_port`, `node_id`, `user_agent`, `cseq`, `raw_sip`.

## Capture sources

clowk-hep3 is **source-agnostic**: it accepts HEP3 from anything that
speaks the protocol over UDP `9060` (or TCP via `HEP_TCP_ADDR`). HEP3 is an
open wire format, so the capture edge is pluggable. Two models exist:

- **In-process export** (Kamailio, FreeSWITCH, OpenSIPS …) — the proxy
  emits a HEP copy of each message itself. Sees **TLS/SIPS decrypted** and
  never drops a packet, at the cost of touching the proxy config.
- **On-wire sniffer** ([heplify](https://github.com/sipcapture/heplify)) — a
  separate agent captures packets off the interface and re-emits them as
  HEP3. Zero config on the proxy, but it **cannot see encrypted** TLS/SIPS
  and may drop packets under load.

Whatever the source: force **HEP3**, send to `udp:<HEP_HOST>:9060`, and —
for B2BUA correlation — make the header named by `HEP_CID` (default `X-CID`)
ride on both legs so the reader can stitch the whole call.

| Source             | Model      | Sees TLS | Force HEP3 with                             |
| ------------------ | ---------- | -------- | ------------------------------------------- |
| Kamailio           | in-process | yes      | `sipcapture` / `siptrace` (`hep_version 3`) |
| FreeSWITCH (sofia) | in-process | yes      | `;hep=3` on `capture-server`                |
| heplify            | sniffer    | no       | default                                     |

### Kamailio (in-process)

```
loadmodule "sipcapture.so"
modparam("sipcapture", "capture_on",     1)
modparam("sipcapture", "hep_capture_id", 100)
modparam("sipcapture", "db_url",         "hep:udp:<HEP_HOST>:9060")

request_route { sip_capture(); ... }
onreply_route { sip_capture(); }
```

The `siptrace` module is an alternative (`hep_mode_on 1`, `hep_version 3`,
`duplicate_uri "sip:<HEP_HOST>:9060"`).

### FreeSWITCH — sofia (in-process)

```xml
<param name="capture-server" value="udp:<HEP_HOST>:9060;hep=3;capture_id=200"/>
```

Enable at runtime with `fs_cli -x "sofia global capture on"`.

### heplify (on-wire sniffer)

Run it on the proxy host (or a port-mirror) and point it at the collector;
it speaks HEP3 by default:

```bash
heplify -i any -m SIP -hs <HEP_HOST>:9060 -fi "port 5060"
```

heplify is a separate process talking the open HEP3 protocol, so using it
has no bearing on clowk-hep3's own licensing.

## Development

```bash
make build                                 # static binary
make test                                  # pure-logic tests
TEST_DATABASE_URL=postgres://… make test   # + Postgres-backed writer tests
make docker                                # build the image
```

## License

AGPL-3.0-only © Thadeu Esteves Jr

Network use is distribution: if you run a modified clowk-hep3 as a
service, you must offer its source to users of that service.
