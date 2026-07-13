# Configuration

The sink is configured entirely through environment variables. This file is the
canonical reference for the table; the aspect specs restate the variables they
govern.

| Variable | Default | Description |
|---|---|---|
| `MQTT_BROKER` | — (required) | Broker URL, e.g. `tcp://broker:1883` or `mqtts://broker:8883` (TLS); credentials as URL userinfo, e.g. `tcp://user:pass@broker:1883` |
| `MQTT_TOPIC` | `#` | Subscription topic filter |
| `MQTT_CLIENT_ID` | `archiver` | Client ID (stable ⇒ broker session queues while disconnected) |
| `ARCHIVE_DIR` | `/var/lib/mqtt-archive` | Archive directory |
| `FLUSH_INTERVAL` | `10s` | Buffer flush cadence; `0` = write through per line. Must stay well under 5 minutes — the heartbeat is touched on the flush tick and goes stale at 5 minutes (see [operations](operations.md)) |
| `FSYNC_INTERVAL` | `60s` | fsync cadence of the active file |
| `HEARTBEAT_FILE` | `<ARCHIVE_DIR>/heartbeat` | Liveness file |
| `ZSTD_LEVEL` | `19` | Compression level (batch, once per day) |
| `BUFFER_SIZE` | `10000` | Bounded receive buffer (messages) |
