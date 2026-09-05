# ADR-0001 — No chunk store: a repository must be restorable without Koffr

- **Status**: Accepted
- **Date**: 2026-09-05
- **Relates to**: PD-001, DEC-001, EX-004

## Context

Deduplicating backup engines exist and are importable: Kloset (the store behind
plakar) is a standalone Go module under ISC, and restic offers a comparable
model. Either would provide deduplication, encryption, snapshot verification and
immutability without writing them.

Both store data as content-addressed chunks. That format is only readable by the
tool that wrote it. A backup tool whose output cannot be read without the tool
itself reintroduces exactly the dependency backups exist to remove: if the
binary is unavailable, incompatible, or abandoned, the data is unreachable even
though it is intact.

## Decision

Koffr writes a plain, documented layout: one object per artifact, compressed
with zstd, encrypted with age, described by a JSON manifest. Every backup can be
restored with `age`, `zstd`, `tar` and the database's own tools.

## Consequences

- A restore is possible on a machine with no Koffr binary, using a generated
  `RESTORE.md` that carries the exact commands for that specific backup.
- The layout can be inspected, audited and scripted against by third-party
  tooling.
- **The price**: no deduplication between backups. Storage cost is roughly the
  sum of the backups kept, not the size of their distinct content.
- That cost is partly offset by PostgreSQL 17+ incremental backups and by WAL
  and binlog archiving, which already avoid re-storing unchanged data.
- The claim is enforced, not asserted: a test restores from a bare machine using
  only the generated `RESTORE.md`, and it runs at every milestone that
  introduces a backup kind.

## Alternatives rejected

- **Kloset as the storage engine** — opaque chunk store, contradicts PD-001. It
  remains possible later as an opt-in `Storage` implementation (EX-004), for
  operators who explicitly accept losing the no-lock-in property in exchange for
  deduplication.
- **A custom deduplicating format** — same drawback as Kloset, with the added
  cost of writing and auditing it.
- **Deduplication delegated to the storage backend** — real on some appliances,
  absent on ordinary S3, so it cannot be relied upon.
