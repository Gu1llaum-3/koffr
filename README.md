# Koffr

Agentless backup for PostgreSQL and MariaDB: streamed end to end, encrypted with
age, and restorable **without Koffr**.

> **Status: early development.** Milestone M0 — interfaces and project scaffolding
> only. Nothing is implemented yet, and the binary does not perform backups.
> Do not use this for anything you care about.

## What it is meant to be

Koffr does not reimplement backup. It drives the tools PostgreSQL and MariaDB
already ship — `pg_basebackup`, `pg_receivewal`, `pg_dump`, `mariabackup`,
`mariadb-dump`, `mariadb-binlog` — and takes care of what they leave out:
scheduling, transport, encryption, storage, retention, verification and
restitution.

- **Agentless.** Nothing is installed on the database host. Databases that are
  not exposed are reached through an SSH tunnel.
- **Streamed.** Backups never touch the disk of the database host or of the Koffr
  host. Memory use stays bounded regardless of database size.
- **Encrypted with age.** Always for at least two recipients: an operational key,
  and an offline recovery key Koffr never holds.
- **Verified.** Integrity checks with no dependencies, and optional real restores
  into a throwaway container. A backup that has not been verified is a hypothesis.
- **No lock-in.** Every backup ships a generated `RESTORE.md` with the exact
  commands to restore it using only standard tools.
- **CLI first.** The web UI is ephemeral, read-only and has no user accounts;
  remote access is `ssh -L`.

## Planned scope

| Engine | Physical | Incremental | Logical | PITR |
|---|---|---|---|---|
| PostgreSQL 14–18 | `pg_basebackup`, streamed | 17+ | `pg_dump -Fc` | WAL archiving |
| MariaDB 10.6–12 | `mariabackup` over SSH | — | `mariadb-dump` | binlog streaming |

Destinations: S3 and compatibles, local or mounted filesystem, SFTP.

## Design

Decisions that shaped the project, and the alternatives they rule out, are
recorded in [`adr/`](./adr):

- [ADR-0001](./adr/0001-no-chunk-store.md) — no deduplicating chunk store; a
  repository must be restorable without Koffr
- [ADR-0002](./adr/0002-age-encryption.md) — age for encryption
- [ADR-0003](./adr/0003-read-only-ephemeral-ui.md) — read-only ephemeral web UI,
  no user accounts
- [ADR-0004](./adr/0004-sqlite-catalog-as-cache.md) — SQLite catalog, treated as a
  cache
- [ADR-0005](./adr/0005-config-file-is-source-of-truth.md) — the configuration
  file is the single source of truth
- [ADR-0006](./adr/0006-builtin-scheduler.md) — built-in scheduler, every job also
  runnable one-shot

## Building

Requires Go 1.24 or later.

```sh
make build     # bin/koffr
make test
make lint
make cross     # linux/amd64 and linux/arm64, statically linked
```

## Prior art

Koffr exists because no single tool covered the whole need. It contains no
third-party source code; the projects below were studied as prior art and are
credited in [`NOTICE`](./NOTICE).

- [wal-g](https://github.com/wal-g/wal-g) — physical backups and WAL, battle-tested,
  but no web UI, no restore verification, and it runs on the database host
- [Databasus](https://github.com/databasus/databasus) — remote operation over SSH,
  verification by real restore, but a permanent multi-user UI and MariaDB in
  logical form only
- [plakar](https://github.com/PlakarKorp/plakar) — the on-demand UI model and a
  strong CLI, but general-purpose rather than database-aware

## Licence

[Apache-2.0](./LICENSE).
