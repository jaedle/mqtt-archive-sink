# Ingestion

How messages move from the broker into the process: connection lifecycle,
subscription, and the bounded receive buffer.

Governed configuration: `MQTT_BROKER`, `MQTT_USERNAME`, `MQTT_PASSWORD`,
`MQTT_TOPIC`, `MQTT_CLIENT_ID`, `BUFFER_SIZE`.

## Connection

Connect over MQTT 3.1.1 with `CleanSession=false` and subscribe to `MQTT_TOPIC`
at QoS 1. The initial connect retries forever — the container may start before
the broker is reachable. Auto-reconnect continues forever with backoff, so the
process stays up across broker loss; the subscription is re-established on every
(re)connect. A stable `MQTT_CLIENT_ID` keeps the broker session, which queues
messages while the sink is disconnected. Connect and reconnect failures are
logged at error level so a permanently failing broker (bad URL, TLS failure)
is visible, not just `connected:false` in the stats line.

`mqtts://` (and `ssl://`/`tls://`) URLs use TLS with the system CA bundle,
which the container image ships. A broker certificate signed by a private CA
requires mounting that CA into `/etc/ssl/certs/`.

## Authentication

Brokers that require authentication take credentials as `MQTT_USERNAME` and
`MQTT_PASSWORD`, sent as the MQTT username and password in the CONNECT packet.
`MQTT_USERNAME` on its own is allowed, for brokers that authenticate on the
username only. `MQTT_PASSWORD` without `MQTT_USERNAME` is rejected at startup: a
password is only ever sent alongside a username, so it would otherwise be
silently dropped.

Credentials must not appear in `MQTT_BROKER`. A URL carrying userinfo, e.g.
`tcp://user:pass@broker:1883`, is rejected at startup — the broker URL is logged
on connect, and MQTT client libraries let URL userinfo silently override the
configured credentials. Note that anything with access to the process
environment can read `MQTT_PASSWORD`.

## Receive and buffer

Each received message is serialized to a record line (see
[archival](archival.md)) and pushed into a bounded channel of `BUFFER_SIZE`
messages. When the buffer is full the receiver blocks, applying backpressure so
the broker session queues the backlog (QoS 1). Messages are acked on receive,
giving at-least-once delivery end to end: a crash loses at most the in-memory
buffer plus any unflushed bytes.
