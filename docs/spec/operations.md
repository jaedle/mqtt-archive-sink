# Operations

Liveness, logging, process lifecycle, subcommands, and the acceptance contract.

Governed configuration: `HEARTBEAT_FILE`, `FLUSH_INTERVAL`, `METRICS_LISTEN_ADDR`.

## Flush and stats tick

Every `FLUSH_INTERVAL` the process flushes the write buffer, touches
`HEARTBEAT_FILE`, and emits one stats record (lines, bytes, skipped, buffer
depth, connected, reconnects). The tick fires even with zero traffic — the
heartbeat is a process-liveness signal, independent of data flow. With
`FLUSH_INTERVAL=0`, writes go through unbuffered and the heartbeat/stats tick runs
at 10s.

## Heartbeat

`HEARTBEAT_FILE`: an empty file whose mtime is the last flush tick. Freshness
contract: an mtime older than 5 minutes means unhealthy (used by the `health`
subcommand and external alerting). Because the heartbeat is touched only on
the flush tick, `FLUSH_INTERVAL` must stay well under 5 minutes — above that,
a healthy process reports unhealthy.

## Metrics

Opt-in, off by default: setting `METRICS_LISTEN_ADDR` (e.g. `:9090`) serves
Prometheus metrics at `GET /metrics`; when empty no listener is opened. The
endpoint exposes application metrics only — no Go runtime or process
collectors:

| Metric | Type | Meaning |
|---|---|---|
| `mqtt_archive_sink_lines_total` | counter | lines accepted into the write buffer |
| `mqtt_archive_sink_bytes_total` | counter | bytes of accepted lines incl. trailing newline |
| `mqtt_archive_sink_skipped_total` | counter | records skipped for exceeding the 16 MiB limit |
| `mqtt_archive_sink_repaired_total` | counter | crash-truncated lines terminated on file open |
| `mqtt_archive_sink_reconnects_total` | counter | broker connection losses |
| `mqtt_archive_sink_connected` | gauge | 1 while connected to the broker, else 0 |
| `mqtt_archive_sink_buffered_messages` | gauge | messages waiting in the receive buffer |

The metric set mirrors the stats log record 1:1. An unbindable address fails
startup (fatal, before connecting to the broker); serve errors after a
successful bind are logged and the sink keeps archiving. The listener shuts
down gracefully with the process.

## Logging

All logging goes through `log/slog` with the JSON handler on stderr. On startup
the process emits one `starting` record including the build version (`dev` for
untagged builds). One stats record is emitted per flush tick.

## Shutdown

On SIGTERM/SIGINT the process disconnects from the broker, drains the buffer,
flushes, fsyncs, and exits 0.

## Hot-path disk errors

A write, flush, fsync, or rotation error on the active file is logged and the
process exits non-zero. The container restart policy is the retry, and the broker
session queue covers the gap. Compression sweep errors are logged and the process
keeps running (see [compression](compression.md)).

## Retention

There is none: the archive grows forever, and the sink never deletes anything
except a plain daily file whose verified `.zst` replacement exists (see
[compression](compression.md)). Pruning old days is external (cron, manual).
Deleting closed `.zst` days is safe — the write path never touches days other
than the current one, and an MCP cursor pointing at a deleted day errors,
instructing the client to restart (see [mcp](mcp.md)).

## Subcommands

| Command | Behavior |
|---|---|
| `mqtt-archive-sink` | run the sink |
| `mqtt-archive-sink verify [dir]` | decode-check every `.zst` (skips current UTC date); flag plain `.ndjson` older than one day (compression stuck). Report per file; non-zero exit on any failure. Dir defaults to `ARCHIVE_DIR`. |
| `mqtt-archive-sink health` | exit 0 iff `HEARTBEAT_FILE` mtime is younger than 5 minutes; for Docker `HEALTHCHECK` (scratch image, no shell) |

## Acceptance

A day of real traffic (~50 MB in) → one `.zst` a few MB, `verify` green,
`zstdcat | wc -l >=` broker message count (QoS 1 is at-least-once ⇒ duplicates
possible; oversized records are skipped), heartbeat never older than the flush
interval while the process runs.
