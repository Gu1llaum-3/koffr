# ADR-0005 — The configuration file is the single source of truth

- **Status**: Accepted
- **Date**: 2026-09-05
- **Relates to**: PD-005, DEC-005, EF-100, HP-006

## Context

Configuration can live in a file, in a database edited through a UI, or in both.
"Both" is the common choice and the one that fails: the file and the database
drift, and neither is trusted.

Koffr's configuration includes which databases exist, how they are reached,
where backups go and how long they are kept. Getting it wrong is not a display
bug; it silently stops protecting a database.

## Decision

A YAML file is the single source of truth. The CLI and the web UI read that
state and never contradict it. The UI does not write configuration (HP-006).

Secrets are referenced by environment variable or by external file path, so the
configuration file itself can be committed to Git.

## Consequences

- The configuration is reviewable, diffable and versionable; a change to backup
  policy goes through the same review as a change to code.
- No divergence is possible between what the UI shows and what is applied.
- Validation is total and happens at load: schema, cross-references,
  reachability of sources and destinations, presence of client binaries,
  availability of the verification runtime. `koffr config validate` runs it
  without starting anything, with an exit code usable in CI (PD-006).
- **The price**: changing anything requires file access. There is no "add a
  database from the browser". For a tool run by the people who administer the
  hosts, that is an acceptable trade; for a self-service product it would not be.
- A UI that writes back into the file remains possible later, but it needs a
  YAML writer that preserves comments and ordering, which is why it is not v1.

## Alternatives rejected

- **Database as the source of truth, file as initial import** — the Databasus
  model. Good for self-service, incompatible with GitOps review of the policy
  that governs the backups.
- **Both writable** — guaranteed drift.
- **Environment variables only** — unworkable for a nested, multi-source
  configuration.
