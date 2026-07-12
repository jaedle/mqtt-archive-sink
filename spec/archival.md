# Archival

The record format and the append-only writing of the current daily file:
serialization, writing, durability, rotation, and mid-line repair.

Governed configuration: `ARCHIVE_DIR`, `FLUSH_INTERVAL`, `FSYNC_INTERVAL`.

## Record format

One JSON object per line, UTF-8, terminated by `\n`:

| Field | Type | Presence |
|---|---|---|
| `ts` | string, RFC3339Nano, UTC | always |
| `topic` | string | always |
| `payload` | string | iff payload is valid UTF-8 |
| `payload_b64` | string, standard base64 | iff payload is not valid UTF-8 |

Exactly one of `payload`/`payload_b64` is present, so the round-trip is lossless.
The maximum serialized record size is 16 MiB; a larger record is counted, logged,
and skipped.

## Writing

A single writer goroutine appends `line + "\n"` to the current daily file, opened
`O_APPEND|O_WRONLY|O_CREATE`. The write path is strictly append-only and writes
only the current daily file. Records whose serialized size exceeds 16 MiB are
counted, logged, and skipped.

## Durability

The write buffer is flushed every `FLUSH_INTERVAL` (with `FLUSH_INTERVAL=0`,
writes go through unbuffered per line). The active file is fsynced every
`FSYNC_INTERVAL`. This bounds the durability window: on power loss it is
`FSYNC_INTERVAL`; on process crash it is `FLUSH_INTERVAL`.

## Rotation

When the current UTC date differs from the open file's date (a `!=` comparison,
which also handles clock steps backwards), the writer flushes, fsyncs, closes,
opens the new date's file, and signals the compression sweeper. Rotating the
write fd is cheap and synchronous; compression runs separately (see
[compression](compression.md)).

## Append repair

On open, if the active daily file exists and ends without a trailing `\n` (a
crash mid-line), a `\n` is appended. The partial line stays archived as its own
line — archive garbage is kept rather than dropped — and is counted in stats.
This is append-only compatible: the file is never truncated.

## Active daily file

`<ARCHIVE_DIR>/YYYY-MM-DD.ndjson` — the date is UTC at write time. Plain NDJSON,
append-only. It may transiently end mid-line after a crash and is repaired to its
own line on the next start (see Append repair).
