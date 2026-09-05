# Koffr — working conventions

Koffr backs up PostgreSQL and MariaDB. It is agentless, streams end to end,
encrypts with age, and is designed so that any backup can be restored **without
Koffr**.

The project is at milestone **M0**: interfaces only, nothing implemented. Do not
assume a package has behaviour just because its interface exists.

## Non-negotiable rules

### Language

English everywhere in this repository: code, comments, identifiers, error
messages, commit messages, documentation, ADRs.

The specification documents (`cahier-des-charges.md`, `architecture.md`,
`roadmap.md`, `plan.md`) are French working documents kept **outside** the
repository, next to the checkout and listed in `.gitignore`. Read them if they
are present; never commit them.

### No third-party source code

wal-g, Databasus and plakar are prior art. Studying their approach is expected
and their architectural ideas are reused deliberately. **Copying their source is
not.** Reimplement from the documented behaviour of the native tools
(`pg_basebackup`, `mariabackup`, and the rest). Record borrowed architecture in
an ADR, not in a comment.

### Requirement references

Behaviour is specified with stable identifiers: `PD-xxx` (principles), `EF-xxx`
(functional), `ENF-xxx` (non-functional), `CT-xxx` (constraints), `DEC-xxx`
(decisions). Cite them in comments where a reader would otherwise wonder why the
code is shaped that way. They are the reason a comment can be short.

## The principles that decide arguments

When a design question comes up, these settle it. They are not aspirations.

1. **PD-001 — No lock-in.** Every backup is restorable with `age`, `zstd`, `tar`
   and the database's own tools. Anything that makes the stored format opaque is
   rejected, however convenient. See `adr/0001-no-chunk-store.md`.
2. **PD-002 — Agentless, but ready for an agent.** Nothing is installed on the
   database host. All remote work goes through `internal/executor`. A future
   agent must be one more `Executor` implementation and nothing else — if a
   change would force `internal/source` or the pipeline to know how the target
   is reached, that change is wrong.
3. **PD-003 — Stream end to end.** No backup writes a complete artifact to disk,
   on the database host or on the Koffr host. Memory use is bounded and
   independent of database size.
4. **PD-004 — Security by default, without a fortress.** No credential in a
   process argument list, in a log, or in an error message. No authentication
   system of our own: it is delegated to SSH and to the OS.
5. **PD-006 — Fail early and loudly.** Anything detectable — a missing client
   binary, insufficient privileges, an unreachable destination, no container
   runtime — is checked when the configuration loads, never discovered mid-job.
6. **PD-007 — An unverified backup is not a backup.** Silence is never success.

## Code conventions

- Go 1.24. `CGO_ENABLED=0` always: a single statically linked binary is a
  requirement (ENF-030), which rules out any C-backed dependency.
- Errors wrap with `%w` and carry a class (`catalog.ErrorClass`). The class
  decides whether an operation is retried; the message never does.
- Error messages name what failed and what the operator should do. They never
  contain a credential, a connection string with a password, or a raw command
  line that might hold one.
- Every long operation takes a `context.Context` and honours cancellation. A
  process started on a target is bound to that context, so cancelling actually
  kills it rather than leaving an orphan holding a replication slot.
- Comments explain *why*, and especially why an obvious alternative is wrong.
  Do not restate the code.

### Concurrency in the pipeline

The pipeline is where the hard bugs live. These invariants are load-bearing;
each one corresponds to a real deadlock or leak:

- The byte counter sits on the **storage** branch, never on the manifest branch.
  A stalled upload is exactly the failure worth detecting.
- Any goroutine on a tee branch calls `CloseWithError` on exit, without
  exception. `io.MultiWriter` is sequential and fail-fast, so a dead branch
  starves the other one's reader unless it is unblocked explicitly.
- Teardown is symmetric: close both pipe writers, drain both result channels,
  including on the early-return path where the process never started.
- Cancellation carries a typed cause, so a failure can be attributed to the
  right actor instead of surfacing as an unhelpful `context canceled`.

## Layout

```
cmd/koffr/          entry point
internal/
  executor/         reaching a machine — local, SSH, later an agent (EX-001)
  source/           making an engine emit a stream — PostgreSQL, MariaDB
  storage/          object stores; layout.go owns every repository path
  catalog/          job history and backup inventory (a cache, not the truth)
  crypto/           age sealing and opening
  verify/           the three verification tiers
  notify/           webhooks, email, dead man's switch
adr/                architecture decision records
docs/spikes/        probe reports
spikes/             throwaway probe code, excluded from build and lint
```

`internal/storage/layout.go` is the **only** place repository paths are defined.
Never build a key by hand elsewhere.

## Commands

```sh
make build     # bin/koffr
make test      # go test ./...
make lint      # go vet + golangci-lint
make cross     # linux/amd64 and linux/arm64, CGO disabled
make vuln      # govulncheck
```

## Things that are settled — do not reopen without an ADR

- No deduplicating chunk store (ADR-0001).
- age for encryption, always at least two recipients, one of them offline
  (ADR-0002).
- Read-only ephemeral web UI, no user accounts (ADR-0003).
- SQLite catalog, treated as a cache of the repository (ADR-0004).
- The configuration file is the single source of truth (ADR-0005).
- Built-in scheduler, every job also runnable one-shot (ADR-0006).

## Known constraints, already accepted

- Koffr shells out to client binaries it cannot embed, and `pg_dump` must be at
  least the server's version. Supporting PostgreSQL 14 to 18 means five client
  toolchains. The binary stays static; the toolchain comes from the host (paths
  configurable per major version) or from the official container image.
- `mariabackup` reads the data directory directly, so MariaDB physical backup
  requires SSH exec on the host. Without it, MariaDB is still fully covered by
  logical backup plus binlog streaming, which is enough for PITR.
- `pg_basebackup` writing tar to stdout forbids `--wal-method=stream`; `fetch`
  plus a persistent replication slot is used instead.
- Restoring a MariaDB physical backup requires `--prepare`, which needs the
  uncompressed backup on disk. This is the tool's design, not something to work
  around.
