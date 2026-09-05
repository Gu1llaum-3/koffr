# ADR-0004 — SQLite catalog, treated as a cache

- **Status**: Accepted
- **Date**: 2026-09-05
- **Relates to**: DEC-004, EF-140, EF-141, EF-142, EX-005, HP-007

## Context

Koffr needs to record job history, a backup inventory and verification reports.
The obvious candidates are SQLite in a local file, or an external PostgreSQL.

PostgreSQL looks attractive for a containerised deployment, where local state is
awkward. But a backup tool that depends on an external database depends on
something that itself needs backing up — and that may well be down at the exact
moment a restore is needed. The dependency is circular in the worst possible
circumstances.

There is also a deeper point: the metadata engine is not what makes the catalog
durable. Replication of the catalog into the repository is.

## Decision

SQLite, behind a `MetadataStore` interface. The catalog is explicitly a **cache**:
the repository is the source of truth. It is replicated into the repository on
every job, and `koffr catalog sync` rebuilds it from scratch.

## Consequences

- No external dependency, no bootstrap ordering problem, no circular dependency.
- Compatible with a single statically linked binary: `modernc.org/sqlite` is
  pure Go, so `CGO_ENABLED=0` holds.
- Losing the Koffr node loses nothing. Rebuild works at two levels: replay the
  replicated catalog, or — if that is lost too — walk the `sources/` prefixes
  and rebuild from the plaintext manifests, which needs no key and no prior
  state. This is why manifests stay in plaintext.
- Kubernetes deployment is a `StatefulSet` with `replicas: 1` and a
  `ReadWriteOnce` volume. That single-writer constraint is one we want anyway:
  two schedulers would run every job twice.
- A pod starting with an empty volume recovers by syncing, so the persistent
  volume is a convenience rather than a durability requirement.
- **The price**: no active/active high availability. Adding it means leader
  election, not merely a shared database — which is why EX-005 is gated behind
  HP-007 rather than being a drop-in backend swap.
- **A hard constraint**: never place the SQLite file on NFS. Its file locking is
  unreliable there and the database will be corrupted.

## Alternatives rejected

- **PostgreSQL as the primary catalog** — circular dependency, external
  operational burden, and it does not deliver HA on its own.
- **No catalog, reading the repository every time** — correct but unusably slow
  for listing and for retention over thousands of objects.
- **Catalog only in the repository** — every CLI command would need network
  access to the destination.
