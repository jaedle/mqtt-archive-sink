# AGENTS.md

## Source of truth

`SPEC.md` is the entry point; it indexes the aspect specs under `spec/` that
define all behavior and file formats. **Change the spec first, then make the code
follow.** Tests assert the spec, not the implementation.

## Layout

| Path | Responsibility | Spec |
|---|---|---|
| `main.go` | env config, subcommand dispatch (`run` default, `verify`, `health`) | [configuration](spec/configuration.md), [operations](spec/operations.md) |
| `internal/app` | wiring: MQTT client → bounded channel → writer; flush/fsync ticks; shutdown; sweep trigger | [operations](spec/operations.md) |
| `internal/archive` | append-only writer of the current daily file: rotation, mid-line repair, flush/fsync | [archival](spec/archival.md) |
| `internal/compress` | background sweep: write `.zst` → verify byte-identical → delete plain; idempotent | [compression](spec/compression.md) |
| `internal/mqtt` | paho wrapper (auto-reconnect, resubscribe on connect), record serialization | [ingestion](spec/ingestion.md) |

## Verification

`task ci` = fmt + lint + test + build. **No external dependencies**: no docker,
no broker, no network — e2e tests run against an embedded in-process
mochi-mqtt broker. Run it before considering any change done.

## Git workflow

**Never commit on `main`.** Every change follows branch → commit → push → PR:

1. Branch from fresh `main`: `git switch main && git pull`, then
   `git switch -c <type>/<short-kebab-slug>` (e.g. `feat/add-xyz`).
   Types are conventional-commit types: `feat`, `fix`, `docs`, `refactor`,
   `test`, `chore`, `perf`, `ci`.
2. Do the work; done = `task ci` green (see Verification). That is this
   repo's pre-commit check — not pnpm.
3. Commit with the `/commit` skill (emoji conventional commits).
4. Push: `git push -u origin <branch>`.
5. Raise the PR immediately, ready for review:
   `gh pr create --base main --title "<conventional title>" --body "<summary + verification>"`.
   Report the PR URL.

## Conventions

- Config via env vars only (Docker deployment) — see [spec/configuration.md](spec/configuration.md)
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
