# Operations

Liveness, logging, process lifecycle, subcommands, and the acceptance contract.

Governed configuration: `HEARTBEAT_FILE`, `FLUSH_INTERVAL`.

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

## Logging

All logging goes through `log/slog` with the JSON handler on stderr. One stats
record is emitted per flush tick.

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
