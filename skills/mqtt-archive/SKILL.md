---
name: mqtt-archive
description: Query archived MQTT messages via the mqtt-archive MCP server (list_days, query, tail). Use when investigating MQTT message history, debugging what a device published, or tailing live MQTT traffic from the archive.
---

# Using the mqtt-archive MCP

The server is a read-only view of an NDJSON archive of every MQTT message.
The tool schemas tell you the parameters; this is what they don't tell you.

## Records

- Days are **UTC**. A late-evening local message lands in the next UTC day —
  when a message seems missing, check the adjacent day.
- Payloads that are not valid UTF-8 come as `payload_b64` (base64) instead of
  `payload`. Handle both.
- Delivery is QoS 1 (at-least-once): duplicates are normal. Count with `>=`,
  never `==`.
- `invalid_lines` counts crash-damaged lines that were skipped. Nonzero is
  informational, not an error.

## query

- One UTC day per call, hard limit. Multi-day investigation: `list_days`
  first, then one `query` per day.
- On continuation calls pass `next_cursor` **and repeat all other parameters
  unchanged** — changing `topic`/`from`/`to` mid-pagination is an error or
  silently wrong.
- Narrow with the `topic` MQTT filter (`+`/`#`) server-side instead of
  fetching `#` and filtering yourself.

## tail

- The first call (no cursor) intentionally returns **no records** — it hands
  you a "start from now" cursor. Publish/act, then poll.
- New messages appear only after the sink's flush interval (default 10s);
  polling faster than that is wasted calls.
- Empty `records` with `has_more: true` means the cursor rolled to a new day —
  call again immediately, don't sleep.
- A cursor error naming a missing day means the day was deleted: restart
  without a cursor.

## Bulk export

Don't paginate `query` to dump a whole day. Use the plain-HTTP download on the
same host/port (same bearer token, always uncompressed):

```sh
curl -H "Authorization: Bearer $TOKEN" http://<host>:8080/days/2026-07-11.ndjson
```

## Scope

Read-only: it cannot publish, delete, or see messages from before the sink was
deployed or after its subscription filter. `list_days` `state` other than
`final` (`active`/`pending`/`untrusted`) just reflects the compression
lifecycle — all states are readable.
