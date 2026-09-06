# Koffr — working conventions

Koffr backs up PostgreSQL and MariaDB. It is agentless, streams end to end,
encrypts with age, and is designed so that any backup can be restored **without
Koffr**.

The project is at milestone **M0**: interfaces only, nothing implemented. Do not
assume a package has behaviour just because its interface exists.

The M0 probes are done and their findings are binding: read
`docs/spikes/M0-report.md` before implementing any of the pipeline.

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

- Go 1.26 (required by `golang.org/x/crypto`, which `internal/executor/ssh`
  depends on). `CGO_ENABLED=0` always: a single statically linked binary is a
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
make ci        # everything the GitHub workflow runs, in about 4 seconds
make hooks     # install the git hooks, once per clone

make build     # bin/koffr
make test      # go test -race ./...
make cover     # coverage
make lint      # go vet + golangci-lint, for this host and for linux/amd64+arm64
make cross     # linux/amd64 and linux/arm64, CGO disabled
make vuln      # govulncheck
```

`make lint` runs golangci-lint three times: for this machine, and for
linux/amd64 and linux/arm64. Build-tagged files are invisible to a linter
running under another GOOS, and CI caught a Linux-only finding a macOS run
could not see. Two seconds here beats a round trip there.

`make ci` runs **everything the GitHub workflow runs**, locally, in about four
seconds. Nothing in the pipeline needs GitHub, so a red build should never be a
surprise. Discovering a lint failure in CI is a wasted round trip.

Git hooks are managed by lefthook; run `make hooks` once per clone. They are
split by cost:

| Hook | Checks | Budget |
|---|---|---|
| `pre-commit` | gofmt (auto-fixes and re-stages), golangci-lint, go vet, key material | < 1 s |
| `commit-msg` | subject length, no trailing period, blank line before the body | instant |
| `pre-push` | `go test -count=1 -race`, cross-compilation, govulncheck | a few seconds |

`--no-verify` skips them. That is a valid escape hatch, not a failure of the
setup; say why in the commit message when you use it.

The hook scripts are themselves tested (`test/hooks`). They are safety controls,
and the secret guard already failed open once: BSD grep read a pattern starting
with `-` as an option, so the private key armour it exists to catch passed
silently. A control that fails open is worse than no control, because it
produces confidence instead of protection.

The toolchain is pinned in `mise.toml`: Go, golangci-lint, lefthook and
govulncheck. Run `mise install` once, and `make` resolves each tool from PATH or
from mise, whichever has it. A missing tool is a hard failure rather than a
skip: a security check that reports success because it never ran is the same
failure mode as a backup that reports success because it never started.

`go.mod` also carries a `toolchain` directive, so the pinned Go version applies
even when mise is not activated. Bumping it is how we respond to a govulncheck
finding: four standard-library issues were open in 1.26.0 and closed by 1.26.6,
and the check is what surfaced them.

Golden files are regenerated with `UPDATE_GOLDEN=1 go test ./...`. Review the
diff before committing: a golden test updated without being read is a test that
has stopped testing anything.

### Integration tests and containers

Tests that need a real PostgreSQL, MinIO or sshd use testcontainers.
`testutil.EnsureDockerHost` asks the docker CLI which daemon it is talking to,
so Colima, Rancher Desktop, Podman and any non-default context work without
anyone exporting `DOCKER_HOST`.

Locally, a missing daemon skips those tests. In CI, `KOFFR_REQUIRE_DOCKER=1`
turns it into a failure: a silently skipped integration suite is
indistinguishable from a passing one, and that is how a backend ships without
ever having been exercised.

## Things that are settled — do not reopen without an ADR

- No deduplicating chunk store (ADR-0001).
- age for encryption, always at least two recipients, one of them offline
  (ADR-0002).
- Read-only ephemeral web UI, no user accounts (ADR-0003).
- SQLite catalog, treated as a cache of the repository (ADR-0004). It is
  replicated into the repository after every job, and `koffr catalog sync`
  rebuilds it — from the replica, or from the plaintext manifests when there
  is no key at all. Never on NFS: SQLite's locking is unreliable there.
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

## Rules established by the M0 probes

These are measured facts, not opinions. `docs/spikes/M0-report.md` has the
evidence.

- **Streamed physical backup requires a single tablespace.** `pg_basebackup`
  refuses `--pgdata=-` on a cluster with a non-default tablespace. `Probe` must
  count tablespaces and reject the configuration at load time. It also creates
  the output file before failing, so never create a destination object before
  the source stream has produced its first byte.
- **Skip `pg_wal/<24 hex chars>` when reconstructing a manifest.** WAL segments
  carried in the tar are absent from `backup_manifest`; `WAL-Ranges` covers
  them. Everything else under `pg_wal/` belongs in the manifest.
- **Never trust `tar.Header.Format`.** It reports `<unknown>` on PostgreSQL's
  archives even though they are strict USTAR.
- **Do not add buffering around age.** A 1 MiB buffer measurably slows the
  chain: zstd already emits large blocks. age sustains 1.3 GiB/s on its own.
- **Write `.pgpass` after the tunnel is bound.** libpq matches it on host *and*
  port, and the tunnel port is chosen by the kernel. Writing it earlier fails
  with a misleading "no password supplied". libpq verifies the certificate
  against the `-h` value, not `PGHOSTADDR`, so TLS `verify-full` keeps working.
- **A generated `RESTORE.md` must not use `set -o pipefail`.** `pg_restore`
  stops reading at the archive end marker, so the decompressor exits 141
  (SIGPIPE) on a perfectly successful restore.
