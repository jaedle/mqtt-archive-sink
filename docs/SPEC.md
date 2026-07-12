# mqtt-archive-sink — Specification

**This document is the single source of truth.** Implementation changes start here:
edit the spec first, then make the code follow. Tests assert this spec.

## Purpose

Subscribe to an MQTT broker and archive every message as one NDJSON line into
daily files, compressing each closed day to a verified zstd archive. A single
process owns the whole pipeline: MQTT connection, buffering, writing, rotation,
compression, flush policy, and heartbeat.

## Aspects

The specification is split into self-contained aspect files; each is true on its
own.

| Aspect | Scope |
|---|---|
| [Configuration](spec/configuration.md) | Environment-variable configuration |
| [Ingestion](spec/ingestion.md) | MQTT connection, subscription, bounded receive buffer |
| [Archival](spec/archival.md) | Record format, append-only daily-file writing, durability, rotation, repair |
| [Compression](spec/compression.md) | Background zstd sweep and the verified archive format |
| [Operations](spec/operations.md) | Liveness, logging, lifecycle, subcommands, acceptance |
