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

Broker credentials go in the URL: `MQTT_BROKER=tcp://user:pass@broker:1883`.
Keep `FLUSH_INTERVAL` well under 5 minutes — the heartbeat is touched on the
flush tick and the health check fails once it is older than 5 minutes.

## Reading archives

Files are named by the **UTC** date at write time, so late-evening local
messages may land in the next day's file.

```sh
cat 2026-07-12.ndjson          # today (plain)
zstdcat 2026-07-11.ndjson.zst  # closed days
```

One JSON object per line; payloads that are not valid UTF-8 carry
`payload_b64` (base64) instead of `payload`:

```json
{"ts":"2026-07-12T14:03:07.123456789Z","topic":"home/sensor/temp","payload":"21.5"}
```

## MCP read access

A separate read-only server, `jaedle/mqtt-archive-mcp`, exposes the archive to
coding agents: MCP tools (`query`, `tail`, `list_days`) over streamable HTTP
plus a whole-day download that always streams uncompressed NDJSON. Static
bearer-token auth; see [docs/spec/mcp.md](docs/spec/mcp.md).

### Hosting (docker compose)

Full example in [deployments/docker-compose.yaml](deployments/docker-compose.yaml)
— sink and MCP server share one archive volume, the MCP side mounted
read-only:

```sh
cd deployments
MCP_AUTH_TOKEN=$(openssl rand -hex 32) docker compose up -d

curl -H "Authorization: Bearer $MCP_AUTH_TOKEN" \
  http://localhost:8080/days/2026-07-11.ndjson   # whole day, uncompressed
# MCP endpoint for agents: http://localhost:8080/mcp
```

### Connecting a local agent (stdio → remote)

Clients that speak streamable HTTP connect directly — e.g. Claude Code:

```sh
claude mcp add --transport http mqtt-archive http://archive-host:8080/mcp \
  --header "Authorization: Bearer $MCP_AUTH_TOKEN"
```

Clients that only spawn stdio servers bridge with
[`mcp-remote`](https://www.npmjs.com/package/mcp-remote) (the `${AUTH_HEADER}`
indirection avoids a known issue with spaces in args on some clients):

```json
{
  "mcpServers": {
    "mqtt-archive": {
      "command": "npx",
      "args": [
        "-y", "mcp-remote",
        "http://archive-host:8080/mcp",
        "--header", "Authorization:${AUTH_HEADER}"
      ],
      "env": { "AUTH_HEADER": "Bearer <MCP_AUTH_TOKEN>" }
    }
  }
}
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
- There is no built-in retention: the archive grows forever. Prune old `.zst`
  files externally (cron, manual) — deleting closed days is safe; the sink
  never touches days other than the current one.
