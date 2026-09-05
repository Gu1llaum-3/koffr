# Spikes — throwaway probe code (milestone M0)

Everything in this directory answers a question and is then thrown away. It is
excluded from the build, from the linter and from the coverage requirement.

**None of this code is reused.** A probe that works is not a starting point: it
was written without tests, without error handling, and without the concurrency
invariants the real pipeline depends on. Reusing it would smuggle all three
omissions into the codebase.

The questions being answered, and the criteria that decide what each answer
means, are tracked outside this repository. The findings land in
`docs/spikes/M0-report.md`.

| Probe | Question |
|---|---|
| P-001 | Does `mariabackup --backup --stream=xbstream` write to the MariaDB host's disk, and how much? |
| P-002 | Can `backup_manifest` be reconstructed reliably by walking the tar in flight? |
| P-003 | Is streaming age encryption a throughput bottleneck? |
| P-004 | Does redirecting libpq through an SSH tunnel break TLS verification or `.pgpass` resolution? |
| P-005 | Does `pg_basebackup --incremental` accept a reconstructed parent manifest? |
| P-006 | What are the limits of restoring a `-Fc` dump from stdin? |
| P-007 | What does `pg_basebackup --pgdata=-` do on a cluster with several tablespaces? |
