# mqtt-archive-sink — Specification

**This document is the single source of truth.** Implementation changes start here:
edit the spec first, then make the code follow. Tests assert this spec.

## Purpose

Subscribe to an MQTT broker, archive every message as one NDJSON line into daily
files, compress closed days with zstd. One process owns: MQTT connection,
buffering, writing, rotation, compression, flush policy, heartbeat.

## Non-goals

- No HTTP/API
- No payload parsing or validation
- No retention/deletion (~1 GB/yr — keep forever)
- No config file (env vars only)
- No protection against concurrently running instances

## Configuration (environment variables only)

| Variable | Default | Description |
|---|---|---|
| `MQTT_BROKER` | — (required) | Broker URL, e.g. `tcp://broker:1883` |
| `MQTT_TOPIC` | `#` | Subscription topic filter |
| `MQTT_CLIENT_ID` | `archiver` | Client ID (stable ⇒ broker session queues while disconnected) |
| `ARCHIVE_DIR` | `/var/lib/mqtt-archive` | Archive directory |
| `FLUSH_INTERVAL` | `10s` | Buffer flush cadence; `0` = write through per line |
| `FSYNC_INTERVAL` | `60s` | fsync cadence of the active file |
| `HEARTBEAT_FILE` | `<ARCHIVE_DIR>/heartbeat` | Liveness file |
| `ZSTD_LEVEL` | `19` | Compression level (batch, once per day) |
| `BUFFER_SIZE` | `10000` | Bounded receive buffer (messages) |

## Behavior

1. **Connect**: MQTT 3.1.1, `CleanSession=false`, QoS 1 subscribe to `MQTT_TOPIC`.
   Initial connect retries forever (the container may start before the broker).
   **Auto-reconnect forever** with backoff; the process never exits on broker loss.
   Subscription is re-established on every (re)connect.
2. **Receive → buffer**: each message is serialized to a record line (see File
   formats) and pushed into a bounded channel of `BUFFER_SIZE`. A full buffer
   blocks the receiver ⇒ backpressure ⇒ the broker session queues (QoS 1).
   Messages are acked on receive (at-least-once end-to-end; a crash loses at most
   the in-memory buffer + unflushed bytes).
3. **Write**: a single writer goroutine appends `line + "\n"` to the current
   daily file. The file is opened `O_APPEND|O_WRONLY|O_CREATE` — the write path
   is **strictly append-only** and **only ever writes the current daily file**.
   Records whose serialized size exceeds **16 MiB** are counted, logged, and
   skipped.
4. **Flush tick** (`FLUSH_INTERVAL`): flush the write buffer, touch
   `HEARTBEAT_FILE`, emit one stats record (lines, bytes, skipped, buffer depth,
   connected, reconnects). Ticks fire even with zero traffic — the heartbeat is
   a **process-liveness** signal, not a data-flow signal. With
   `FLUSH_INTERVAL=0` writes go through unbuffered and the heartbeat/stats tick
   runs at 10s.
5. **fsync tick** (`FSYNC_INTERVAL`): fsync the active file. Durability window
   on power loss = `FSYNC_INTERVAL`; on process crash = flush interval.
6. **Rotation**: when the current UTC date differs from the open file's date
   (`!=` comparison — also handles clock steps backwards): flush, fsync, close,
   open the new date's file, signal the sweeper. Rotation of the write fd is
   cheap and synchronous; compression is not part of it.
7. **Compression sweep** (background, single worker; triggered at startup and
   after each rotation): for every plain `.ndjson` whose date is older than
   today:
   1. write `<name>.ndjson.zst` (overwrites any leftover),
   2. fsync the `.zst`,
   3. decode the `.zst` and byte-compare against the plain file,
   4. fsync the directory,
   5. delete the plain file.

   **On mismatch or any error the plain file is never deleted**; the failure is
   logged and the sweep continues with the next file. A plain file next to a
   `.zst` marks the `.zst` untrusted ⇒ it is redone on the next sweep. The sweep
   is **idempotent** — safe to interrupt and re-run at any point. It never
   touches today's file ⇒ no contention with the writer.
8. **Append repair**: if the active daily file exists and does not end with
   `\n` (crash mid-line), a `\n` is appended on open. The partial line stays
   archived as its own line (archive garbage rather than drop it), counted in
   stats. Append-only compatible — no truncation anywhere.
9. **Shutdown** (SIGTERM/SIGINT): disconnect from broker, drain the buffer,
   flush, fsync, exit 0.
10. **Hot-path disk errors** (write/flush/fsync/rotate of the active file):
    log, exit non-zero. The container restart policy is the retry; the broker
    session queue covers the gap. Sweep errors do **not** terminate the process
    (see 7).

## File formats

### Record

One JSON object per line, UTF-8, terminated by `\n`:

| Field | Type | Presence |
|---|---|---|
| `ts` | string, RFC3339Nano, UTC | always |
| `topic` | string | always |
| `payload` | string | iff payload is valid UTF-8 |
| `payload_b64` | string, standard base64 | iff payload is not valid UTF-8 |

Exactly one of `payload`/`payload_b64` is present. Round-trip is lossless.
Maximum serialized record size: 16 MiB (larger ⇒ skipped, counted).

### Active daily file

`<ARCHIVE_DIR>/YYYY-MM-DD.ndjson` — date is UTC at write time. Plain NDJSON,
append-only. May transiently end mid-line after a crash; repaired to its own
line on next start (see Behavior 8).

### Closed archive

`<ARCHIVE_DIR>/YYYY-MM-DD.ndjson.zst` — a **single zstd frame** with content
checksum, decoding byte-identical to the plain file it replaced. Readable with
stock `zstdcat`, checkable with `zstd -t`.

### Heartbeat

`HEARTBEAT_FILE`: empty file; mtime = last flush tick. Freshness contract:
**mtime older than 5 minutes ⇒ unhealthy** (used by `health` and external
alerting).

### Directory states (sweep contract)

| State | Meaning |
|---|---|
| `.ndjson` only | pending compression |
| `.ndjson` + `.ndjson.zst` | `.zst` untrusted — will be redone |
| `.ndjson.zst` only | final, verified |

## Subcommands

| Command | Behavior |
|---|---|
| `mqtt-archive-sink` | run the sink |
| `mqtt-archive-sink verify [dir]` | decode-check every `.zst` (skips current UTC date); flag plain `.ndjson` older than one day (compression stuck). Report per file; non-zero exit on any failure. Dir defaults to `ARCHIVE_DIR`. |
| `mqtt-archive-sink health` | exit 0 iff `HEARTBEAT_FILE` mtime is younger than 5 minutes; for Docker `HEALTHCHECK` (scratch image, no shell) |

## Logging

All logging via `log/slog`, JSON handler, stderr. One stats record per flush
tick.

## Acceptance

A day of real traffic (~50 MB in) → one `.zst` a few MB, `verify` green,
`zstdcat | wc -l >=` broker message count (QoS 1 is at-least-once ⇒ duplicates
possible; oversized records are skipped), heartbeat never older than the flush
interval while the process runs.
