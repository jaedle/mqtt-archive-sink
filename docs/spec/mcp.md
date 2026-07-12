# MCP read access

A second, read-only process (`mqtt-archive-mcp`) exposes the archive to coding
agents for debugging: an MCP server (streamable HTTP) with query/tail/list
tools plus a plain-HTTP whole-day download. It shares `ARCHIVE_DIR` with the
sink (mounted read-only in its container), never writes, and never connects to
the broker. It is a LAN/localhost debugging aid, not an internet-facing API.

Record and file formats are defined by [archival](archival.md) and
[compression](compression.md); this aspect only reads them.

## Deployment

Shipped as its own image, `jaedle/mqtt-archive-mcp` (the `mcp` Dockerfile
target; the sink stays `jaedle/mqtt-archive-sink`). Run it as a second
container with the archive volume mounted read-only.

## Configuration (environment variables only)

| Variable | Default | Description |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `AUTH_TOKEN` | — (required) | Static bearer token; the process refuses to start without it |
| `ARCHIVE_DIR` | `/var/lib/mqtt-archive` | Archive directory (read-only) |

## HTTP surface

All endpoints except `/healthz` require `Authorization: Bearer <AUTH_TOKEN>`
(constant-time comparison; anything else is `401`).

| Route | Behavior |
|---|---|
| `/mcp` | MCP streamable-HTTP endpoint (tools below) |
| `GET /days/YYYY-MM-DD.ndjson` | Streams the whole day as **uncompressed** NDJSON (`application/x-ndjson`) regardless of how it is stored, decoding `.zst` on the fly. `404` unknown day, `400` malformed date. |
| `GET /healthz` | `200`, unauthenticated; for container healthchecks |

## MCP tools

### `list_days`

No input. Returns every day present in the archive:

```json
{"days":[{"date":"2026-07-11","state":"final","size_bytes":2413567,"compressed":true}]}
```

`state` is derived from the directory states in [compression](compression.md)
plus the writer: `active` (plain file, current UTC date), `pending` (plain,
older), `untrusted` (plain + `.zst`), `final` (`.zst` only). No line counts —
that would require decoding every day.

### `query`

Historical range scan with pagination.

Input: `from`/`to` (RFC3339, optional), `topic` (MQTT topic filter with `+`/`#`
wildcards, default `#`), `limit` (1–1000, default 100), `cursor` (opaque, from
a previous response; non-cursor parameters must be repeated on continuation
calls).

Output:

```json
{"records":[{"ts":"...","topic":"...","payload":"..."}],"next_cursor":"...","has_more":true,"invalid_lines":0}
```

Records are returned in archive order. Day files are selected by filename date
within `[from, to]`; each record is then filtered by its `ts` and the topic
filter. Records use exactly the archived field set (`payload` or
`payload_b64`).

### `tail`

Cursor-based polling for new events. Input: `cursor` (optional), `topic`
(optional), `limit` (optional). Output shape is identical to `query`.

The first call (no cursor) returns no records and a cursor positioned at the
current end of the archive ("start from now"); subsequent calls return only
lines appended since the given cursor. Polling survives day rotation and
compression of the polled day.

## Cursor semantics

A cursor addresses the next unread line as `(day, complete-line index)` and is
opaque to clients (base64url JSON, versioned). It stays valid across:

- **append** — files are append-only and never truncated,
- **rotation** — the cursor rolls to the next day at line index 0,
- **compression** — the `.zst` decodes byte-identical to the plain file it
  replaced, so line indexes are stable.

A cursor may carry a byte-offset hint into the plain file as a pure
optimization for tailing the active day; when the plain file is gone or too
short, the reader falls back to decoding and skipping lines. A cursor pointing
at a day that no longer exists (manual deletion) is an error instructing the
client to restart without a cursor.

## Reading rules

- When both `YYYY-MM-DD.ndjson` and `.ndjson.zst` exist, read the plain file —
  the `.zst` is untrusted per the sweep contract.
- Only complete `\n`-terminated lines are ever emitted (tools and download
  alike). A partially flushed or crash-torn trailing line is invisible until
  its `\n` arrives; a cursor never advances past one.
- Lines that are not valid record JSON (e.g. crash-repaired garbage) are
  skipped, counted per response as `invalid_lines`, and still advance the
  cursor.
- Reads take no locks: the writer only appends, and the sweep's plain-file
  deletion cannot tear an already-open file descriptor; a fresh open falls back
  to the `.zst`.

## Subcommands

| Command | Behavior |
|---|---|
| `mqtt-archive-mcp` | run the server |
| `mqtt-archive-mcp health` | exit 0 iff `GET /healthz` on the configured `LISTEN_ADDR` port returns 200; for Docker `HEALTHCHECK` (scratch image, no shell) |

## Logging

All logging via `log/slog`, JSON handler, stderr.

## Acceptance

Against an archive with compressed and active days: `list_days` shows every
day with correct states; a paginated `query` over a compressed day returns
exactly its records; a `tail` poll loop observes a live-published message
within one flush interval and keeps working across a day rotation; downloading
a compressed day yields byte-identical content to `zstdcat`; every authorized
route returns `401` without the token.
