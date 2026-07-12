# mqtt-archive-sink

Archives every MQTT message as NDJSON into daily files; closed days are
compressed to verified zstd archives. Behavior is defined in [SPEC.md](docs/SPEC.md)
— the single source of truth.

## Run

```sh
docker run -d --restart always \
  -e MQTT_BROKER=tcp://broker:1883 \
  -v mqtt-archive:/var/lib/mqtt-archive \
  jaedle/mqtt-archive-sink
```

## Configuration

| Variable | Default |
|---|---|
| `MQTT_BROKER` | — (required) |
| `MQTT_TOPIC` | `#` |
| `MQTT_CLIENT_ID` | `archiver` |
| `ARCHIVE_DIR` | `/var/lib/mqtt-archive` |
| `FLUSH_INTERVAL` | `10s` (`0` = write per line) |
| `FSYNC_INTERVAL` | `60s` |
| `HEARTBEAT_FILE` | `<ARCHIVE_DIR>/heartbeat` |
| `ZSTD_LEVEL` | `19` |
| `BUFFER_SIZE` | `10000` |

## Reading archives

```sh
cat 2026-07-12.ndjson          # today (plain)
zstdcat 2026-07-11.ndjson.zst  # closed days
```

## MCP read access

A separate read-only server, `jaedle/mqtt-archive-mcp`, exposes the archive to
coding agents: MCP tools (`query`, `tail`, `list_days`) over streamable HTTP
plus a whole-day download that always streams uncompressed NDJSON. Static
bearer-token auth; see [docs/spec/mcp.md](docs/spec/mcp.md).

```sh
docker run -d --restart always \
  -e AUTH_TOKEN=change-me \
  -v mqtt-archive:/var/lib/mqtt-archive:ro \
  -p 8080:8080 \
  jaedle/mqtt-archive-mcp

curl -H 'Authorization: Bearer change-me' \
  http://localhost:8080/days/2026-07-11.ndjson   # whole day, uncompressed
# MCP endpoint for agents: http://localhost:8080/mcp
```

## Subcommands

- `mqtt-archive-sink verify [dir]` — decode-check all archives (skips current
  date), flag stuck compressions; non-zero exit on failure
- `mqtt-archive-sink health` — heartbeat freshness (used by the Docker
  `HEALTHCHECK`)

## Development

```sh
mise install
task test      # unit + embedded-broker tests — Docker-free, fast
task test:e2e  # real dockerized broker + sink image — needs Docker
task ci        # fmt + lint + test + test:e2e + build (needs Docker)
```

Two test layers: `task test` runs against an embedded in-process MQTT broker (no
external dependencies); `task test:e2e` (build tag `e2e`,
[test/e2e](test/e2e)) starts a real mosquitto broker and the actual sink image via
Docker Compose and asserts a published message lands in the archive and is
served back over MCP. It is parallel-safe — each run uses a unique compose
project name and no fixed host ports — so many can run at once on one machine.
Both layers run in CI, managed by
[pipeline-service](https://github.com/jaedle/pipeline-service) via
`ci/config.yaml`; release pushes `jaedle/mqtt-archive-sink` and
`jaedle/mqtt-archive-mcp` to Docker Hub.

Tests follow arrange/act/assert as blank-line-separated blocks (no comment
labels), keep bodies short by extracting named helpers, and name every
timeout/size constant.

Changes go branch → PR, never directly to `main` — see the git workflow in
[AGENTS.md](AGENTS.md).

## Deployment notes

- QoS 1 is at-least-once: duplicates are possible, so message-count checks
  should use `>=`, never `==`.
- Outage coverage while the sink is down is bounded by the broker's queue for
  persistent sessions (mosquitto: `max_queued_messages`, default 1000 — raise
  it).
