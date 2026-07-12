# AGENTS.md

## Source of truth

`SPEC.md` defines all behavior and file formats. **Change the spec first, then
make the code follow.** Tests assert the spec, not the implementation.

## Layout

| Path | Responsibility |
|---|---|
| `main.go` | env config, subcommand dispatch (`run` default, `verify`, `health`) |
| `internal/app` | wiring: MQTT client → bounded channel → writer; flush/fsync ticks; shutdown; sweep trigger |
| `internal/archive` | append-only writer of the current daily file: rotation, mid-line repair, flush/fsync |
| `internal/compress` | background sweep: write `.zst` → verify byte-identical → delete plain; idempotent |
| `internal/mqtt` | paho wrapper (auto-reconnect, resubscribe on connect), record serialization |

## Verification

`task ci` = fmt + lint + test + build. **No external dependencies**: no docker,
no broker, no network — e2e tests run against an embedded in-process
mochi-mqtt broker. Run it before considering any change done.

## Conventions

- Config via env vars only (Docker deployment) — see table in SPEC.md
- Logging: `log/slog`, JSON handler, stderr
- Tests: `testify`; TDD at the agreed seams: archive writer, app e2e
  (broker→file), reconnect e2e. Mostly high-level tests; lower-level only for
  details unreachable from e2e (fault injection, encoding corners). Never test
  the same behavior twice.
- Invariants: write path only appends, and only to the current daily file;
  plain files are never deleted unless a verified byte-identical `.zst` exists
- CI: jaedle/pipeline-service reads `ci/config.yaml`; verify job runs
  `task ci`, release job runs `task release` (pushes `jaedle/mqtt-archive-sink`
  to Docker Hub)
