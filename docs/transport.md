# Transport — HEP3 over UDP and TCP

clowk-hep3 accepts HEP3 on **both UDP and TCP**. Both listeners can run at
once; pick per source based on reliability and security needs.

## UDP vs TCP

| | UDP (`HEP_ADDR`) | TCP (`HEP_TCP_ADDR`) |
| --- | --- | --- |
| Default | on (`0.0.0.0:9060`) | off (opt-in) |
| Reliability | best-effort — can drop under load/congestion, **especially across hosts** | reliable, ordered, length-framed — no silent loss |
| Overhead | minimal | connection + ACKs |
| Use when | same-host / low-latency LAN, very high PPS where occasional loss is acceptable | **cross-VM / WAN**, or whenever you can't afford to lose messages |

**Recommendation:** for **cross-VM** capture (SBC on one server, collector on
another), prefer **TCP** — it won't silently drop SIP messages under network
congestion. Keep UDP enabled too if some sources only speak UDP.

## Enabling TCP

UDP and TCP are distinct protocols, so both listeners can share port `9060`:

```bash
HEP_ADDR=0.0.0.0:9060        # UDP (default)
HEP_TCP_ADDR=0.0.0.0:9060    # TCP (opt-in — set to enable)
```

On voodu (collector deployment):

```hcl
ports = [
  "0.0.0.0:9060:9060/udp",   # UDP ingest
  "0.0.0.0:9060:9060/tcp",   # TCP ingest (recommended for cross-VM)
]

env = {
  HEP_TCP_ADDR = "0.0.0.0:9060"
}
```

The TCP listener reads a stream of **length-framed** HEP3 packets (the 2-byte
total-length header frames each one). A corrupt / out-of-frame stream is
dropped (the connection resets) rather than mis-parsed.

## Cross-VM topology

HEP is a **network protocol** — the collector does **not** need to live on the
SBC. Run the capture source on the SBC host and point it at the collector:

```
SRV-1 (SBC)                              SRV-2 (collector)
Kamailio / FreeSWITCH ──HEP3/tcp:9060 (network)──▶ clowk-hep3
  (siptrace / sipcapture / heplify)
```

See [Capture sources](../README.md#capture-sources) for per-source config
(Kamailio, FreeSWITCH, heplify).

## Firewall

Open, from the SBC to the collector:

- `tcp/9060` (if using TCP — recommended)
- `udp/9060` (if using UDP)

Prefer a **private network / VPC** between the SBC and the collector.

## Security (PII)

SIP signaling carries PII (numbers, identities, headers). clowk-hep3 speaks
**plaintext** HEP over UDP/TCP — there is **no built-in TLS**. Therefore:

- Keep HEP traffic on a **private network / VPC**. Never send cleartext HEP
  across the public internet.
- If HEP must cross an untrusted network, **tunnel it** (WireGuard / IPsec /
  SSH) or terminate TLS at a proxy in front of the collector.
- Native HEP-over-TLS is a future item.

## Shared volume (ndjson) — REQUIRED

On the `ndjson` path the collector (clowk-hep3) and the reader
([voodu-hep3](https://github.com/thadeu/voodu-hep3)) **share one named
docker volume** — that volume *is* the writer→reader handoff (it replaces a
shared database). This imposes hard requirements:

- **Same host.** Docker named volumes are host-local, so collector and
  reader must run on the **same server** (e.g. SRV-2). The SBC can be
  elsewhere — it reaches the collector over the network via HEP.
- **Same volume name.** Both reference it: collector
  `volumes = ["hep3-data:/data"]` (read-write); reader
  `volumes = ["hep3-data:/data:ro"]` (read-only, emitted by the plugin's
  `expand` from the `data_volume` field — default `hep3-data`).
- **Same uid.** NDJSON files are `0600` (SIP is PII), so the reader must
  run as the **same uid as the collector**. Both images pin **uid 10001** —
  don't override the user.
- **Ownership bootstrap.** The collector image ships `/data` owned by
  `hep:hep` (uid 10001) and declares `VOLUME /data`, so docker initializes
  a *fresh* named volume with that ownership. If you pre-create the volume
  (or reuse one owned by another uid), `chown` it to `10001:10001` first.

On the `pg` path there is **no shared volume** — collector and reader only
share `DATABASE_URL` and may run on different hosts.

## Configuration reference

All env vars are documented in the [Configuration](../README.md#configuration)
table in the README. The transport-relevant ones:

| Var | Default | Description |
| --- | --- | --- |
| `HEP_ADDR` | `0.0.0.0:9060` | UDP listen address |
| `HEP_TCP_ADDR` | _(disabled)_ | TCP listen address — set to enable |
