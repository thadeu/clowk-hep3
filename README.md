# clowk-hep3

**HEP3 capture collector — receive SIP, write to Postgres.**

`clowk-hep3` receives [HEP3](https://github.com/sipcapture/HEP) datagrams
from a SIP proxy (Kamailio, FreeSWITCH, OpenSIPS …), extracts the SIP
signaling, and **writes it to a shared Postgres**. It is the WRITER /
collector half of the SIP-capture stack.

It serves no API. The read-only REST API (call lists, ladder diagrams,
stats) is a separate, independently-deployable service —
[voodu-hep3](https://github.com/thadeu/voodu-hep3) — that connects to the
**same `DATABASE_URL`**. Collector and reader can run on different
servers against one database (e.g. an RDS).

> HEP3 is captured as a **side-channel**: your SIP proxy sends a copy of
> each message to clowk-hep3 in parallel; it never sits in the call path.

## Quick start

You provide the Postgres (RDS, voodu-postgres, a container — anything) and
pass its URL:

```bash
docker run -p 9060:9060/udp \
  -e DATABASE_URL=postgres://user:pass@host:5432/hep \
  ghcr.io/thadeu/clowk-hep3:latest
```

clowk-hep3 runs its migrations on boot (it owns the schema), then listens
for HEP3 on UDP 9060 and writes parsed SIP into the `sip_messages` table.
Point your SIP proxy's HEP capture at `udp:<host>:9060`.

On voodu, deploy it as a plain deployment — see
[examples/hep3-server.voodu](examples/hep3-server.voodu).

## Configuration

Everything is an env var with a working default. `DATABASE_URL` is the
only required one. A local `.env` is loaded for development.

| Var                  | Default        | Description                                   |
| -------------------- | -------------- | --------------------------------------------- |
| `DATABASE_URL`       | _(required)_   | shared Postgres connection string             |
| `HEP_ADDR`           | `0.0.0.0:9060` | UDP listen address for HEP3 datagrams         |
| `HEP_TCP_ADDR`       | _(disabled)_   | optional TCP listen address for HEP3          |
| `HEP_DB_BULK`        | `200`          | insert batch size                             |
| `HEP_DB_TIMER`       | `4s`           | max wait before flushing a partial batch      |
| `HEP_DB_WORKERS`     | `4`            | parallel decode workers                       |
| `HEP_RETENTION_DAYS` | `30`           | drop messages older than this (0 = keep all)  |
| `HEP_CID`            | `X-CID`        | SIP header used to stitch B2BUA leg pairs     |
| `HEP_EXCEPT_METHODS` | `OPTIONS`      | comma-separated SIP methods dropped on ingest |

## Schema

One JSONB `data` column holds the full record; the query-hot fields
(`ts`, `call_id`, `x_cid`, `method`, `response_code`, `from_user`,
`to_user`, `cseq`) are STORED generated columns over `data->>'...'`, and
indexed. Adding a captured field is just a JSON key — a migration is only
needed when a field becomes query-hot enough to want its own generated
column. Migrations are golang-migrate files under
[infra/migrations](infra/migrations), embedded into the binary.

## Wiring your SIP proxy

### Kamailio

```
loadmodule "sipcapture.so"
modparam("sipcapture", "capture_on",     1)
modparam("sipcapture", "hep_capture_id", 100)
modparam("sipcapture", "db_url",         "hep:udp:<HEP_HOST>:9060")

request_route { sip_capture(); ... }
onreply_route { sip_capture(); }
```

For B2BUA setups that rewrite `Call-ID` between legs, inject a shared
correlation header (default `X-CID`) on both legs so the reader can stitch
the whole call.

### FreeSWITCH

```xml
<param name="capture-server" value="udp:<HEP_HOST>:9060;hep=3;capture_id=200"/>
```

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
