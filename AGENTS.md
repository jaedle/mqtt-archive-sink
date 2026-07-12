# AGENTS.md

## Source of truth

`docs/SPEC.md` is the entry point; it indexes the aspect specs under `docs/spec/`
that define all behavior and file formats. **Change the spec first, then make the
code follow.** Tests assert the spec, not the implementation.

## Layout

Follows [golang-standards/project-layout](https://github.com/golang-standards/project-layout).

| Path | Responsibility | Spec |
|---|---|---|
| `cmd/mqtt-archive-sink/main.go` | env config, subcommand dispatch (`run` default, `verify`, `health`) | [configuration](docs/spec/configuration.md), [operations](docs/spec/operations.md) |
| `internal/app` | wiring: MQTT client → bounded channel → writer; flush/fsync ticks; shutdown; sweep trigger | [operations](docs/spec/operations.md) |
| `internal/archive` | append-only writer of the current daily file: rotation, mid-line repair, flush/fsync | [archival](docs/spec/archival.md) |
| `internal/compress` | background sweep: write `.zst` → verify byte-identical → delete plain; idempotent | [compression](docs/spec/compression.md) |
| `internal/mqtt` | paho wrapper (auto-reconnect, resubscribe on connect), record serialization | [ingestion](docs/spec/ingestion.md) |
| `build/package/Dockerfile` | multi-stage static build → scratch image | — |
| `docs/SPEC.md` | spec index → aspect specs under `docs/spec/` | — |
| `ci/config.yaml` | jaedle/pipeline-service config (must stay at `ci/`) | — |

## Verification

`task ci` = fmt + lint + test + test:e2e + build. Run it before considering any
change done.

- `task test` — unit + embedded in-process mochi-mqtt broker tests. Docker-free,
  no network; the default for fast local iteration.
- `task test:e2e` — real-infra end-to-end (build tag `e2e`): a dockerized
  mosquitto broker + the actual sink image via `test/e2e/docker-compose.yaml`.
  Docker is a **test dependency** here (fine — not a runtime dependency).

**The e2e stack must stay safe to run many times concurrently on one machine.**
Each run uses a unique compose project name (`-p mas-e2e-<pid>`) and its own
archive dir, and **publishes no host ports** — the broker is reached only over
the per-run compose network, and messages are published from inside it. Keep it
that way: no fixed host ports (use the network, or an ephemeral port if ever
unavoidable).

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

- Config via env vars only (Docker deployment) — see [docs/spec/configuration.md](docs/spec/configuration.md)
- Logging: `log/slog`, JSON handler, stderr
- Tests: `testify`; TDD at the agreed seams: archive writer, app e2e
  (broker→file), reconnect e2e. Mostly high-level tests; lower-level only for
  details unreachable from e2e (fault injection, encoding corners). Never test
  the same behavior twice.
- Test style: arrange/act/assert as blank-line-separated blocks (no `// ARRANGE`
  labels); keep bodies short (aim < 20 lines) by pushing polling/decoding/setup
  into named helpers; no anonymous assertion closures; name every timeout/size
  constant with a one-line rationale.
- Invariants: write path only appends, and only to the current daily file;
  plain files are never deleted unless a verified byte-identical `.zst` exists
- CI: jaedle/pipeline-service reads `ci/config.yaml`; verify job runs
  `task ci`, release job runs `task release` (pushes `jaedle/mqtt-archive-sink`
  to Docker Hub)
